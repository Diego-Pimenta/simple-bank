FROM golang:1.24.3-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

FROM alpine:edge
WORKDIR /app
COPY --from=build /app/main .
COPY app.env wait-for.sh ./
COPY db/migration ./db/migration
RUN chmod +x ./wait-for.sh
EXPOSE 8080
CMD ["/app/main"]