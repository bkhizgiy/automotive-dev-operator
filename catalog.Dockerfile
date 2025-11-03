FROM quay.io/operator-framework/opm:latest

ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/configs", "--cache-dir=/tmp/cache"]

ADD catalog /configs
RUN ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache", "--cache-only"]

LABEL operators.operatorframework.io.index.configs.v1=/configs

