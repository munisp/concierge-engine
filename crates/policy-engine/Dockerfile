FROM rust:1.83-alpine AS build
WORKDIR /src
COPY crates/Cargo.toml crates/
COPY crates/policy-engine crates/policy-engine
COPY crates/ledger-adapter crates/ledger-adapter
COPY crates/ingestion-parser crates/ingestion-parser
RUN apk add --no-cache musl-dev && cd crates && cargo build --release -p policy-engine

FROM alpine:3.20
COPY --from=build /src/crates/target/release/policy-engine /bin/policy-engine
EXPOSE 8200
ENTRYPOINT ["/bin/policy-engine"]
