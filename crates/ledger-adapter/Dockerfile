FROM rust:1.83-alpine AS build
WORKDIR /src
COPY crates/Cargo.toml crates/
COPY crates/policy-engine crates/policy-engine
COPY crates/ledger-adapter crates/ledger-adapter
COPY crates/ingestion-parser crates/ingestion-parser
RUN apk add --no-cache musl-dev && cd crates && cargo build --release -p ledger-adapter

FROM alpine:3.20
COPY --from=build /src/crates/ledger-adapter/target/release/ledger-adapter /bin/ledger-adapter
EXPOSE 8210
ENTRYPOINT ["/bin/ledger-adapter"]
