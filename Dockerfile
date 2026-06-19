# Single static binary — air-gap friendly (HLD §4/§14).
FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/agent-platform-backend ./cmd/server

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/agent-platform-backend /agent-platform-backend
EXPOSE 8080
ENTRYPOINT ["/agent-platform-backend"]
