FROM golang:1.24.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG PROJECT=ordercore
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app ./cmd/${PROJECT}
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /app /app
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/app"]
