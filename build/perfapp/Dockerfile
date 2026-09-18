FROM registry.access.redhat.com/ubi9/ubi:latest AS build

ENV GOPATH=/tmp/go \
    GOCACHE=/tmp/.cache \
    PATH=$PATH:/usr/local/go/bin \
    GOLANG_VERSION=go1.26.5 \
    GOLANG_SHA256=5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053 \
    CGO_ENABLED=0 \
    GOPROXY=https://proxy.golang.org,direct

WORKDIR /tmp
RUN curl -Lo ${GOLANG_VERSION}.linux-amd64.tar.gz https://dl.google.com/go/${GOLANG_VERSION}.linux-amd64.tar.gz \
    && echo "${GOLANG_SHA256} ${GOLANG_VERSION}.linux-amd64.tar.gz" > ${GOLANG_VERSION}.linux-amd64.sha256 \
    && sha256sum -c ${GOLANG_VERSION}.linux-amd64.sha256 \
    && tar -C /usr/local -xzf ${GOLANG_VERSION}.linux-amd64.tar.gz \
    && rm -f ${GOLANG_VERSION}.linux-amd64.tar.gz

WORKDIR /src
COPY . .
RUN go build -o /perfapp ./perfapp

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
COPY --from=build /perfapp /usr/local/bin/perfapp
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/perfapp"]
