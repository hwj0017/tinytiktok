package account

import (
	"context"
	"errors"
	"feedsystem_video_go/internal/auth"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"

	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AccountService struct {
	accountRepository *AccountRepository
	cache             *rediscache.Client
}

var (
	ErrUsernameTaken       = errors.New("username already exists")
	ErrNewUsernameRequired = errors.New("new_username is required")
	IdFilter               = "filter:account:id"
	UsernameFilter         = "filter:account:username"
)

func NewAccountService(accountRepository *AccountRepository, cache *rediscache.Client) *AccountService {
	return &AccountService{accountRepository: accountRepository, cache: cache}
}

func (as *AccountService) CreateAccount(ctx context.Context, account *Account) error {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(account.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	account.Password = string(passwordHash)
	if err := as.accountRepository.CreateAccount(ctx, account); err != nil {
		return err
	}
	as.cache.AddCF(ctx, UsernameFilter, account.Username)
	as.cache.AddCF(ctx, IdFilter, strconv.FormatUint(uint64(account.ID), 10))
	return nil
}

func (as *AccountService) Rename(ctx context.Context, accountID uint, newUsername string) (string, error) {

	if newUsername == "" {
		return "", ErrNewUsernameRequired
	}

	newUsernameLower := strings.ToLower(newUsername)

	// 1. 【第一道防线】拦截已被占用的新用户名
	exists, err := as.cache.Rdb.Do(ctx, "CF.EXISTS", UsernameFilter, newUsernameLower).Bool()
	if err != nil && err != redis.Nil {
		// Redis 异常时降级，记录日志并继续走 DB 兜底
		log.Printf("cuckoo filter check failed: %v", err)
	} else if exists {
		// 过滤器说名字已存在，直接返回占用错误，完全不给 DB 压力
		// 注意：这里承担了布谷鸟过滤器极小的误报率（即某个没被占用的名字被误判为占用），
		// 但对于“改名”场景，让用户换一个名字是完全可以接受的安全折中。
		return "", ErrUsernameTaken
	}

	// 2. 【数据准备】获取旧用户名，用于后续从过滤器中剔除
	// 如果你的系统在 Context 或业务外层已经传了旧名字，这一步可以省略以节省性能
	oldAccount, err := as.accountRepository.FindByID(ctx, accountID)
	if err != nil {
		return "", err
	}
	oldUsernameLower := strings.ToLower(oldAccount.Username)

	// 3. 【原有逻辑】生成 Token
	token, err := auth.GenerateToken(accountID, newUsername)
	if err != nil {
		return "", err
	}

	// 4. 【原有逻辑】更新数据库
	if err := as.accountRepository.RenameWithToken(ctx, accountID, newUsername, token); err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return "", ErrUsernameTaken
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", err
		}
		return "", err
	}

	// 5. 【同步过滤器】改名成功，一删一增 (使用 Pipeline 保证性能)
	pipe := as.cache.Rdb.Pipeline()
	pipe.Do(ctx, "CF.DEL", UsernameFilter, oldUsernameLower) // 释放旧名字
	pipe.Do(ctx, "CF.ADD", UsernameFilter, newUsernameLower) // 占坑新名字
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("failed to update cuckoo filter after rename: %v", err)
		// 仅打日志，不要让正常的改名流程失败
	}

	// 6. 【原有逻辑】更新缓存
	if as.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		if err := as.cache.SetBytes(cacheCtx, fmt.Sprintf("account:%d", accountID), []byte(token), 24*time.Hour); err != nil {
			log.Printf("failed to set cache: %v", err)
		}
	}

	return token, nil
}

func (as *AccountService) ChangePassword(ctx context.Context, username, oldPassword, newPassword string) error {
	account, err := as.FindByUsername(ctx, username)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(oldPassword)); err != nil {
		return err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := as.accountRepository.ChangePassword(ctx, account.ID, string(passwordHash)); err != nil {
		return err
	}
	if err := as.Logout(ctx, account.ID); err != nil {
		return err
	}
	return nil
}

func (as *AccountService) FindByID(ctx context.Context, id uint) (*Account, error) {
	idStr := strconv.FormatUint(uint64(id), 10)

	// 1. 在 Redis 中检查 ID 是否存在
	// CF.EXISTS key item
	if as.cache != nil {
		exists, err := as.cache.IsEXIST(ctx, IdFilter, idStr)
		if err != nil && err != redis.Nil {
			// 如果 Redis 挂了，为了保证业务可用性，通常选择“降级”：直接查 DB
			log.Printf("Filter error: %v", err)
		} else if !exists {
			// 确定不存在，直接拦截攻击
			return nil, errors.New("security: account not found")
		}
	}

	// 2. 过滤器说“可能有”，查询数据库
	account, err := as.accountRepository.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	return account, nil
}
func (as *AccountService) FindByUsername(ctx context.Context, username string) (*Account, error) {
	// 1. 定义过滤器专用的 Key (与 ID 过滤器分开，避免冲突)

	// 2. 第一道防线：Redis 布谷鸟过滤器检查
	// username 本身就是字符串，不需要像 ID 那样转换
	exists, err := as.cache.IsEXIST(ctx, UsernameFilter, username)
	if err != nil {
		// 容错处理：如果 Redis 故障，记录日志并降级回数据库查询
		log.Printf("Filter error: %v", err)
	} else if !exists {
		// 过滤器确定不存在该用户名，直接拦截
		return nil, errors.New("user not found")
	}

	// 3. 第二道防线：查询数据库
	account, err := as.accountRepository.FindByUsername(ctx, username)
	if err != nil {
		// 如果数据库也没查到（过滤器的极小概率误报），正常返回错误
		return nil, err
	}

	return account, nil
}

func (as *AccountService) Login(ctx context.Context, username, password string) (string, error) {
	account, err := as.FindByUsername(ctx, username)
	if err != nil {
		return "", err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(password)); err != nil {
		return "", err
	}
	// generate token
	token, err := auth.GenerateToken(account.ID, account.Username)
	if err != nil {
		return "", err
	}
	if err := as.accountRepository.Login(ctx, account.ID, token); err != nil {
		return "", err
	}
	if as.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		if err := as.cache.SetBytes(cacheCtx, fmt.Sprintf("account:%d", account.ID), []byte(token), 24*time.Hour); err != nil {
			log.Printf("failed to set cache: %v", err)
		}
	}
	return token, nil
}

func (as *AccountService) Logout(ctx context.Context, accountID uint) error {
	account, err := as.FindByID(ctx, accountID)
	if err != nil {
		return err
	}
	if account.Token == "" {
		return nil
	}
	if as.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		if err := as.cache.Del(cacheCtx, fmt.Sprintf("account:%d", account.ID)); err != nil {
			log.Printf("failed to del cache: %v", err)
		}
	}
	return as.accountRepository.Logout(ctx, account.ID)
}
