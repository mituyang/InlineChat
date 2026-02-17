FROM alpine:3.20
RUN adduser -D -H appuser
WORKDIR /app
USER appuser
COPY .bin/server /bin/server
COPY public ./public
EXPOSE 8200
ENTRYPOINT ["/bin/server"]
