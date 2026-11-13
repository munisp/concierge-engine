FROM rust:1.83-alpine AS build
WORKDIR /src
COPY crates/Cargo.toml crates/
COPY crates/policy-engine crates/policy-engine
COPY crates/ledger-adapter crates/ledger-adapter
COPY crates/ingestion-parser crates/ingestion-parser
COPY crates/mojaloop-adapter crates/mojaloop-adapter
RUN apk add --no-cache musl-dev && cd crates && cargo build --release -p mojaloop-adapter

FROM alpine:3.20
COPY --from=build /src/crates/mojaloop-adapter/target/release/mojaloop-adapter /bin/mojaloop-adapter
EXPOSE 8220
ENTRYPOINT ["/bin/mojaloop-adapter"]
