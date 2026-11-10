FROM golang:1.24 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /sootcase-report ./cmd/sootcase-report

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /sootcase-report /sootcase-report
EXPOSE 8090
ENV ADDR=:8090
ENTRYPOINT ["/sootcase-report"]
CMD ["serve"]
