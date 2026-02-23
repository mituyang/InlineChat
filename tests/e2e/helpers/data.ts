let seq = 0;

export type SiteSeed = {
  siteID: string;
  siteName: string;
  siteDomain: string;
};

export type AgentSeed = {
  agentID: string;
  email: string;
  password: string;
  displayName: string;
};

export type ScenarioSeed = {
  suffix: string;
  site: SiteSeed;
  agent: AgentSeed;
  visitorToken: string;
};

function nextSeq(): number {
  seq += 1;
  return seq;
}

function normalizeSlug(input: string, max = 24): string {
  const raw = String(input || "").trim().toLowerCase();
  const value = raw
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, max);
  return value || "e2e";
}

function buildAgentID(seed: number): string {
  const value = (Math.abs(seed) % 9000) + 1000;
  return String(value).padStart(4, "0");
}

export function buildScenarioSeed(prefix: string): ScenarioSeed {
  const localSeq = nextSeq();
  const now = Date.now();
  const rand = Math.floor(Math.random() * 1000);

  const suffix = `${normalizeSlug(prefix, 12)}-${localSeq}-${now}-${rand}`;
  const suffixCompact = suffix.replace(/-/g, "");

  const siteID = `site_${suffix}`.slice(0, 64);
  const siteName = `E2E Site ${suffix}`;
  const siteDomain = `${suffix}.e2e.local`.toLowerCase();

  const agentID = buildAgentID(now + localSeq + rand);
  const agentPassword = `Agent#${String(now % 100000).padStart(5, "0")}Aa!`;
  const emailLocal = `${suffixCompact.slice(0, 20)}${agentID}`.toLowerCase();
  const email = `${emailLocal}@example.com`;
  const displayName = `E2E Agent ${agentID}`;

  const visitorToken = `vt_${suffix}`.replace(/[^a-zA-Z0-9_-]/g, "_");

  return {
    suffix,
    site: {
      siteID,
      siteName,
      siteDomain,
    },
    agent: {
      agentID,
      email,
      password: agentPassword,
      displayName,
    },
    visitorToken,
  };
}
