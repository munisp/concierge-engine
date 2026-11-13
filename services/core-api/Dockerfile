FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /bin/core-api ./cmd/core-api

FROM alpine:3.20
COPY --from=build /bin/core-api /bin/core-api
EXPOSE 8000
ENTRYPOINT ["/bin/core-api"]
