ARG BASE_IMAGE=quay.io/jeevandroid/perf-job-base
FROM ${BASE_IMAGE}

RUN GOBIN=/usr/local/bin go install github.com/kubesaw/ksctl/cmd/ksctl@master

WORKDIR /opt/toolchain-e2e
COPY . .
RUN chmod +x build/perf-job/entrypoint.sh \
    && go build -o /usr/local/bin/setup ./setup \
    && ln -sf /opt/toolchain-e2e/build/perf-job/entrypoint.sh /usr/local/bin/entrypoint \
    && git config --system --add safe.directory /opt/toolchain-e2e \
    && mkdir -p /opt/toolchain-e2e/build/_output /tmp/go /tmp/.cache \
    && chgrp -R 0 /opt/toolchain-e2e /tmp/go /tmp/.cache \
    && chmod -R g=u /opt/toolchain-e2e /tmp/go /tmp/.cache

ENTRYPOINT ["/usr/local/bin/entrypoint"]
