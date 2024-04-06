FROM golang:1.18.10-alpine as build-stage

RUN apk --no-cache add \
    g++ \
    git \
    make \
    bash

ARG VERSION
ENV VERSION=${VERSION}

WORKDIR /src
COPY . .
RUN ./scripts/build/build.sh

# Final image.
FROM alpine:latest
RUN apk --no-cache add \
    ca-certificates \
    openssh-client
COPY --from=build-stage /src/bin/k8s-slurm-injector /usr/local/bin/k8s-slurm-injector
ENTRYPOINT ["/usr/local/bin/k8s-slurm-injector"]