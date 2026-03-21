package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	RabbitMQ RabbitMQConfig `yaml:"rabbitmq"`
	// 新增 AI 相关配置
	Asr       AsrConfig       `yaml:"asr"`
	VectorDB  VectorDBConfig  `yaml:"vector_db"`
	Embedding EmbeddingConfig `yaml:"embedding"`
	LLM       LLMConfig       `yaml:"llm_api"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
}

type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type RabbitMQConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}
type AsrConfig struct {
	Addr     string `yaml:"addr"`
	Model    string `yaml:"model"`
	Language string `yaml:"language"`
}

// VectorDBConfig 向量数据库配置 (Qdrant)
type VectorDBConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// EmbeddingConfig 本地向量化配置 (Ollama)
type EmbeddingConfig struct {
	Addr  string `yaml:"addr"`
	Model string `yaml:"model"`
}

// LLMConfig 云端大模型 API 配置 (OpenAI/DeepSeek 等)
type LLMConfig struct {
	Provider string `yaml:"provider"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
}

func Load(filename string) (Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}
