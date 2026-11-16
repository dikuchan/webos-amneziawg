FROM ubuntu:24.04
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends gcc gcc-arm-linux-gnueabihf libc6-dev libc6-dev-armhf-cross make iproute2 iputils-ping procps ca-certificates file && rm -rf /var/lib/apt/lists/*
