FROM ubuntu:16.04

RUN apt-get update || true \
    && apt-get -y install --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY bin/tbpolicy /tbpolicy

CMD ["/tbpolicy"]
