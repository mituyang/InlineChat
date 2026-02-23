import fs from "node:fs";
import path from "node:path";

const rootDir = path.resolve(__dirname, "../../..");
const envFile = process.env.E2E_ENV_FILE?.trim() || path.join(rootDir, ".env");

function readEnvFileValue(filePath: string, key: string): string {
  if (!fs.existsSync(filePath)) {
    return "";
  }

  const prefix = `${key}=`;
  const lines = fs.readFileSync(filePath, "utf8").split(/\r?\n/);
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || !trimmed.startsWith(prefix)) {
      continue;
    }
    const raw = line.slice(line.indexOf("=") + 1).trim();
    if (
      (raw.startsWith('"') && raw.endsWith('"') && raw.length >= 2) ||
      (raw.startsWith("'") && raw.endsWith("'") && raw.length >= 2)
    ) {
      return raw.slice(1, -1).trim();
    }
    return raw;
  }
  return "";
}

const fileGatewayPort = readEnvFileValue(envFile, "GATEWAY_HTTP_PORT");
const fileSuperAdminEmail = readEnvFileValue(envFile, "SUPER_ADMIN_EMAIL");
const fileSuperAdminPassword = readEnvFileValue(envFile, "SUPER_ADMIN_PASSWORD");

const gatewayPort = (process.env.GATEWAY_HTTP_PORT || fileGatewayPort || "").trim() || "8200";

export const e2eEnv = {
  rootDir,
  envFile,
  baseURL: (process.env.E2E_BASE_URL || "").trim() || `http://127.0.0.1:${gatewayPort}`,
  superAdminEmail: (process.env.E2E_SUPER_ADMIN_EMAIL || process.env.SUPER_ADMIN_EMAIL || fileSuperAdminEmail || "").trim(),
  superAdminPassword:
    (process.env.E2E_SUPER_ADMIN_PASSWORD || process.env.SUPER_ADMIN_PASSWORD || fileSuperAdminPassword || "").trim(),
};

export function requireSuperAdminCredentials(): void {
  if (!e2eEnv.superAdminEmail || !e2eEnv.superAdminPassword) {
    throw new Error(
      "缺少超级管理员凭证：请设置 E2E_SUPER_ADMIN_EMAIL / E2E_SUPER_ADMIN_PASSWORD（或 .env 中 SUPER_ADMIN_*）",
    );
  }
}
