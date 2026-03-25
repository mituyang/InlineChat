FROM alpine:3.20
RUN adduser -D -H appuser
WORKDIR /app
RUN mkdir -p /app/docs && chown -R appuser:appuser /app
COPY --chown=appuser:appuser services/ai-service/.bin/server /bin/server
COPY --chown=appuser:appuser docs/qinghe-home-customer-knowledge-base.md ./docs/qinghe-home-customer-knowledge-base.md
USER appuser
EXPOSE 8205
ENTRYPOINT ["/bin/server"]
