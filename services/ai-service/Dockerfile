FROM alpine:3.20
RUN adduser -D -H appuser
WORKDIR /app
RUN mkdir -p /app/data/knowledgebases /app/data/knowledgebases/.inlinechat-state && chown -R appuser:appuser /app
COPY --chown=appuser:appuser services/ai-service/.bin/server /bin/server
USER appuser
EXPOSE 8205
ENTRYPOINT ["/bin/server"]
