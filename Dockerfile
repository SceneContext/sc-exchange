FROM golang:1.24 AS build

ENV GOPROXY="https://proxy.golang.org"
ENV CGO_ENABLED=1

RUN apt-get update && \
    apt-get install -y --no-install-recommends git gcc build-essential && \
    apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

WORKDIR /app/prebid-server/
COPY ./ ./
RUN go mod tidy
RUN go mod vendor
ARG TEST="true"
RUN if [ "$TEST" != "false" ]; then ./validate.sh ; fi
RUN go build -mod=vendor -ldflags "\
    -X github.com/prebid/prebid-server/v4/version.Ver=$(git describe --tags 2>/dev/null | sed 's/^v//' || echo 'dev') \
    -X github.com/prebid/prebid-server/v4/version.Rev=$(git rev-parse HEAD 2>/dev/null || echo 'unknown')" .

FROM ubuntu:22.04 AS release
LABEL maintainer="scenecontext.io"
WORKDIR /usr/local/bin/
COPY --from=build /app/prebid-server/prebid-server .
RUN chmod a+xr prebid-server
COPY static static/
COPY stored_requests/data stored_requests/data
RUN chmod -R a+r static/ stored_requests/data

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl mtr libatomic1 && \
    apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

RUN addgroup --system --gid 2001 prebidgroup && \
    adduser --system --uid 1001 --ingroup prebidgroup prebid
USER prebid
EXPOSE 8000
EXPOSE 6060
ENTRYPOINT ["/usr/local/bin/prebid-server"]
CMD ["-v", "1", "-logtostderr"]
