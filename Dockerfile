# pms-dummy — build a static binary, run it on alpine.
# Alpine (not distroless): the deploy readiness gate runs `wget` INSIDE the
# container, so busybox must be present. Runs as root because the container
# binds :80 to match the cloudflared ingress origin (http://pms-dummy:80).
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pms-dummy .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/pms-dummy /usr/local/bin/pms-dummy
EXPOSE 80
VOLUME /data
ENV PMS_DUMMY_OWN_BASE_URL=""
ENTRYPOINT ["pms-dummy"]
CMD ["-addr", ":80", "-db", "/data/pms-dummy.db"]
