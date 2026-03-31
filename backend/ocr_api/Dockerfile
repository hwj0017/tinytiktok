# 使用官方轻量级 Python 镜像
FROM python:3.9-slim-bullseye

WORKDIR /app

# 替换为清华源，解决国内下载慢的问题
RUN sed -i 's/deb.debian.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apt/sources.list && \
    sed -i 's/security.debian.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apt/sources.list

# 安装底层的 C++ 动态链接库 (OpenCV 需要)
RUN apt-get update && apt-get install -y libgl1-mesa-glx libglib2.0-0 && rm -rf /var/lib/apt/lists/*

COPY . /app

# 使用清华源快速安装 Python 依赖
RUN pip install --no-cache-dir -i https://pypi.tuna.tsinghua.edu.cn/simple \
    fastapi uvicorn python-multipart paddlepaddle paddleocr opencv-python-headless numpy

EXPOSE 8000
CMD ["python", "main.py"]