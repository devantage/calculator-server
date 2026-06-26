# BUILDER

FROM alpine:3 AS builder

RUN apk update

RUN apk add build-base wget

RUN mkdir /source

WORKDIR /source

ENV GO_VERSION="1.26.4"

ARG TARGETOS

ARG TARGETARCH

ENV PLATFORM="${TARGETOS}-${TARGETARCH}"

RUN mkdir .go

RUN wget https://go.dev/dl/go${GO_VERSION}.${PLATFORM}.tar.gz

RUN tar -xzf go${GO_VERSION}.${PLATFORM}.tar.gz --directory /source/.go --strip-components 1

ENV PATH="${PATH}:/source/.go/bin"

COPY . .

RUN make deps

RUN make build

# APP

FROM alpine:3 AS app

RUN mkdir /app

WORKDIR /app

COPY --from=builder /source/dist/calculator-server .

RUN chmod +x ./calculator-server

RUN addgroup app

RUN adduser app --home /app --disabled-password --ingroup app

RUN chown -R app:app /app

USER app

EXPOSE 8080

ENTRYPOINT ["./calculator-server", "-transport=http", "-host=0.0.0.0"]