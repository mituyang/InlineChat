FROM alpine:3.20
RUN adduser -D -H appuser
USER appuser
COPY .bin/server /bin/server
EXPOSE 8203
ENTRYPOINT ["/bin/server"]
