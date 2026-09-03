FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o marketers_portal .

FROM alpine:3.19
RUN apk add --no-cache tzdata ca-certificates poppler-utils tesseract-ocr tesseract-ocr-data-eng imagemagick
WORKDIR /app
COPY --from=builder /app/marketers_portal .
COPY templates/ templates/
COPY static/    static/
RUN mkdir -p uploads
EXPOSE 8080
CMD ["./marketers_portal"]
