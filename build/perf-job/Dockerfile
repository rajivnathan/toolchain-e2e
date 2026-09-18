FROM registry.access.redhat.com/ubi9/ubi:latest

LABEL maintainer="Developer Sandbox <devsandbox@redhat.com>"
LABEL author="Developer Sandbox <devsandbox@redhat.com>"

ENV GOPATH=/tmp/go \
    GOCACHE=/tmp/.cache \
    PATH=$PATH:$GOPATH/bin:/usr/local/go/bin \
    GIT_COMMITTER_NAME="Developer Sandbox" \
    GIT_COMMITTER_EMAIL=devsandbox@redhat.com \
    GOLANG_VERSION=go1.26.5 \
    GOLANG_SHA256=5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053 \
    CGO_ENABLED=0 \
    GOPROXY=https://proxy.golang.org,direct

RUN yum install -y \
        findutils \
        git \
        make \
        tar \
        gzip \
        wget \
        which \
        jq && \
    yum clean all

RUN curl -LO https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest/openshift-client-linux.tar.gz && \
    tar -xzvf openshift-client-linux.tar.gz -C /usr/local/bin oc && \
    chmod +x /usr/local/bin/oc && \
    rm -f openshift-client-linux.tar.gz

WORKDIR /tmp
RUN curl -Lo ${GOLANG_VERSION}.linux-amd64.tar.gz https://dl.google.com/go/${GOLANG_VERSION}.linux-amd64.tar.gz \
    && echo "${GOLANG_SHA256} ${GOLANG_VERSION}.linux-amd64.tar.gz" > ${GOLANG_VERSION}.linux-amd64.sha256 \
    && sha256sum -c ${GOLANG_VERSION}.linux-amd64.sha256 \
    && tar -C /usr/local -xzf ${GOLANG_VERSION}.linux-amd64.tar.gz \
    && rm -f ${GOLANG_VERSION}.linux-amd64.tar.gz \
    && go version \
    && GOBIN=/usr/local/bin go install github.com/kubesaw/ksctl/cmd/ksctl@master

WORKDIR /opt/toolchain-e2e
COPY . .
RUN chmod +x build/perf-job/entrypoint.sh \
    && go build -o /usr/local/bin/setup ./setup \
    && ln -sf /opt/toolchain-e2e/build/perf-job/entrypoint.sh /usr/local/bin/entrypoint

ENTRYPOINT ["/usr/local/bin/entrypoint"]
