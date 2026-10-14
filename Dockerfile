FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS TARGETARCH TARGETVARIANT VERSION=dev
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /lanclip .

FROM scratch
COPY --from=build /lanclip /lanclip
ENV LANCLIP_ADDR=:9501 LANCLIP_DATA=/data
VOLUME /data
EXPOSE 9501
ENTRYPOINT ["/lanclip"]
