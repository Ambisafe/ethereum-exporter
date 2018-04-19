FROM alpine:edge AS build

RUN apk update
RUN apk upgrade
RUN apk add --update go gcc g++ git

WORKDIR /app

ENV GOPATH /app

ADD . /app/src/melonproject/ethereum-exporter

RUN CGO_ENABLED=1 GOOS=linux go get -v -a melonproject/ethereum-exporter

FROM alpine:latest

RUN apk update && apk add ca-certificates curl && rm -rf /var/cache/apk/*

EXPOSE 4000

# Add our application binary
COPY --from=build /app/bin/ethereum-exporter /app/ethereum-exporter

WORKDIR /app

ADD ./config.example.json /app/config.json

ENTRYPOINT ["/app/ethereum-exporter", "--config", "/app/config.json"]