FROM golang:1.27-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /sharefly ./cmd/sharefly && mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /sharefly /sharefly
COPY --from=build --chown=nonroot:nonroot /data /data
VOLUME /data
EXPOSE 8787 8080
ENTRYPOINT ["/sharefly"]
CMD ["server", "--api-addr", "0.0.0.0:8787", "--public-addr", "0.0.0.0:8080", "--data-dir", "/data"]
