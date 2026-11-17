# 运行时镜像：打包本机构建好的产物（make image 会先执行 make build）。
# 二进制在编译期内嵌 web/dist.zip（前端），镜像内无需前端产物。
# 基座 debian:forky-slim：wireshark-common 为 4.6.x，与协议校准基线一致。
FROM debian:forky-slim
# apt 走阿里云镜像源；还原官方源：--build-arg APT_MIRROR=deb.debian.org
ARG APT_MIRROR=mirrors.aliyun.com
RUN sed -i "s|deb.debian.org|${APT_MIRROR}|g" /etc/apt/sources.list.d/debian.sources \
 && apt-get update \
 && apt-get install -y --no-install-recommends wireshark-common \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/webshark /var/lib/webshark
COPY bin/webshark /usr/local/bin/webshark
COPY bin/pktdump /usr/local/bin/pktdump
COPY build/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh
ENV SPOOL=/var/lib/webshark \
    LISTEN=0.0.0.0:8080 \
    DUMPCAP_IFACE=eth0 \
    DUMPCAP_FILESIZE=8388608 \
    DUMPCAP_FILES=16
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
