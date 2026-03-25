FROM node:22-alpine AS console-web-builder
WORKDIR /build/console-web
COPY apps/console-web/package*.json ./
RUN npm ci
COPY apps/console-web ./
RUN npm run build

FROM alpine:3.20
RUN adduser -D -H appuser
WORKDIR /app
RUN mkdir -p /app/public/sdk /app/public/agent /app/public/admin /app/docs/backend && chown -R appuser:appuser /app
COPY --chown=appuser:appuser services/gateway-service/.bin/server /bin/server
COPY --chown=appuser:appuser apps/customer-console ./public/customer
COPY --from=console-web-builder --chown=appuser:appuser /build/console-web/dist/agent ./public/agent
COPY --from=console-web-builder --chown=appuser:appuser /build/console-web/dist/admin ./public/admin
COPY --chown=appuser:appuser apps/staff-login ./public/staff-login
COPY --chown=appuser:appuser apps/widget-chat ./public/widget
COPY --chown=appuser:appuser apps/demo-site ./public/demo
COPY --chown=appuser:appuser apps/api-docs ./public/api-docs
COPY --chown=appuser:appuser apps/widget-sdk/inlinechat-widget.js ./public/sdk/inlinechat-widget.js
COPY --chown=appuser:appuser docs/backend ./docs/backend
USER appuser
EXPOSE 8200
ENTRYPOINT ["/bin/server"]
