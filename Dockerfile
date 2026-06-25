# Single static binary — air-gap friendly (HLD §4/§14).
FROM quay.io/vmware-ai/golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/agent-platform-backend ./cmd/server

FROM quay.io/vmware-ai/debian:12.12-slim
COPY --from=build /out/agent-platform-backend /agent-platform-backend
EXPOSE 8080
ENTRYPOINT ["/agent-platform-backend"]
