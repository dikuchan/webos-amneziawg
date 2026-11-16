FROM ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends gcc gcc-arm-linux-gnueabihf libc6-dev libc6-dev-armhf-cross make iproute2 iputils-ping procps ca-certificates file && rm -rf /var/lib/apt/lists/*
COPY dependencies/musl /source/dependencies/musl
COPY dependencies/amneziawg-tools /source/dependencies/amneziawg-tools
COPY scripts/build-musl-awg.sh scripts/link-awg-musl.sh /source/scripts/
