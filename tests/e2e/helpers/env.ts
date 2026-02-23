import fs from "node:fs";
import path from "node:path";

import dotenv from "dotenv";

const rootDir = path.resolve(__dirname, "../../..");
const envFile = process.env.E2E_ENV_FILE?.trim() || path.join(rootDir, ".env");

if (fs.existsSync(envFile)) {
  dotenv.config({ path: envFile });
}

const gatewayPort = (process.env.GATEWAY_HTTP_PORT || "").trim() || "8200";

export const e2eEnv = {
  rootDir,
  envFile,
  baseURL: (process.env.E2E_BASE_URL || "").trim() || `http://127.0.0.1:${gatewayPort}`,
  superAdminEmail: (process.env.E2E_SUPER_ADMIN_EMAIL || process.env.SUPER_ADMIN_EMAIL || "").trim(),
  superAdminPassword: (process.env.E2E_SUPER_ADMIN_PASSWORD || process.env.SUPER_ADMIN_PASSWORD || "").trim(),
};

export function requireSuperAdminCredentials(): void {
  if (!e2eEnv.superAdminEmail || !e2eEnv.superAdminPassword) {
    throw new Error(
      "缺少超级管理员凭证：请设置 E2E_SUPER_ADMIN_EMAIL / E2E_SUPER_ADMIN_PASSWORD（或 .env 中 SUPER_ADMIN_*）",
    );
  }
}
