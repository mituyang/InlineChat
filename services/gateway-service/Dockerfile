FROM alpine:3.20
RUN adduser -D -H appuser
WORKDIR /app
RUN mkdir -p /app/public/sdk && chown -R appuser:appuser /app
COPY --chown=appuser:appuser services/gateway-service/.bin/server /bin/server
COPY --chown=appuser:appuser apps/customer-console ./public/customer
COPY --chown=appuser:appuser apps/agent-console ./public/agent
COPY --chown=appuser:appuser apps/admin-console ./public/admin
COPY --chown=appuser:appuser apps/staff-login ./public/staff-login
COPY --chown=appuser:appuser apps/widget-chat ./public/widget
COPY --chown=appuser:appuser apps/demo-site ./public/demo
COPY --chown=appuser:appuser apps/widget-sdk/inlinechat-widget.js ./public/sdk/inlinechat-widget.js
USER appuser
EXPOSE 8200
ENTRYPOINT ["/bin/server"]
