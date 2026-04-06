<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";

import ConsoleHeader from "../shared/components/ConsoleHeader.vue";
import MetricCard from "../shared/components/MetricCard.vue";
import PanelCard from "../shared/components/PanelCard.vue";
import StatusPill from "../shared/components/StatusPill.vue";
import { clearStaffToken } from "../shared/auth";
import { apiRequest, APIError } from "../shared/api";
import { formatAgentID, formatTime } from "../shared/format";
import { initTheme, applyTheme } from "../shared/theme";
import type { AdminAgent, MeResponse, Site, SiteAIConfig } from "../shared/types";

const LOGIN_PAGE_URL = "/app/staff-login/";
const NEXT_URL = "/app/admin/";

const theme = ref<"light" | "dark">(initTheme());
const me = ref<MeResponse | null>(null);
const sites = ref<Site[]>([]);
const agents = ref<AdminAgent[]>([]);
const selectedSiteId = ref("");
const selectedAIConfig = ref<SiteAIConfig | null>(null);
const statusText = ref("正在加载管理后台...");
const statusError = ref(false);
const loading = ref(false);
const savingSite = ref(false);
const savingAI = ref(false);
const reloadingAI = ref(false);
const editingSiteId = ref("");
const siteForm = ref({
  site_id: "",
  name: "",
  domains_text: "",
});
const agentForm = ref({
  agent_id: "",
  site_id: "",
  email: "",
  password: "",
  display_name: "",
});

const selectedSite = computed(() => sites.value.find((site) => site.site_id === selectedSiteId.value) ?? null);
const activeAgentCount = computed(() => agents.value.filter((item) => item.status === "active").length);
const isEditingSite = computed(() => editingSiteId.value !== "");
const siteSubmitText = computed(() => {
  if (savingSite.value) {
    return isEditingSite.value ? "保存中..." : "创建中...";
  }
  return isEditingSite.value ? "保存修改" : "创建站点";
});
const sitePanelDescription = computed(() =>
  isEditingSite.value ? `正在编辑 ${editingSiteId.value}，站点 ID 创建后不可修改。` : "结构化重做站点与 AI 入口，避免一个超长脚本同时维护全部表单。",
);
const themeText = computed(() => (theme.value === "dark" ? "亮色模式" : "暗色模式"));
const userText = computed(() => (me.value ? `${me.value.email}` : "未登录"));
const roleText = computed(() => {
  if (!me.value) {
    return "管理角色";
  }
  return me.value.role === "super_admin" ? "超级管理员" : "管理员";
});
const roleTone = computed(() => (me.value?.role === "super_admin" ? "brand" : "default"));

watch(
  () => selectedSiteId.value,
  async (siteId) => {
    if (!siteId) {
      selectedAIConfig.value = null;
      return;
    }
    await loadSiteAIConfig(siteId);
  },
);

onMounted(async () => {
  await bootstrap();
});

async function bootstrap(): Promise<void> {
  loading.value = true;
  try {
    me.value = await apiRequest<MeResponse>("/api/auth/v1/auth/me", { auth: true });
    if (me.value.role !== "admin" && me.value.role !== "super_admin") {
      throw new APIError("当前账号不是管理角色", 403);
    }
    await Promise.all([loadSites(), loadAgents()]);
    if (!selectedSiteId.value && sites.value[0]) {
      selectedSiteId.value = sites.value[0].site_id;
    }
    setStatus("管理后台已就绪");
  } catch (error) {
    handleError(error, "加载管理后台失败");
  } finally {
    loading.value = false;
  }
}

async function loadSites(): Promise<void> {
  const payload = await apiRequest<{ items: Site[] }>("/api/admin/v1/admin/sites?limit=100", { auth: true });
  sites.value = (payload.items ?? []).map((item) => ({
    ...item,
    domains: Array.isArray(item.domains) ? item.domains : [],
  }));
  if (editingSiteId.value) {
    const currentEditingSite = sites.value.find((item) => item.site_id === editingSiteId.value) ?? null;
    if (currentEditingSite) {
      applySiteToForm(currentEditingSite);
    } else {
      resetSiteForm();
    }
  }
  if (!selectedSiteId.value && sites.value.length > 0) {
    selectedSiteId.value = sites.value[0].site_id;
  }
  if (!agentForm.value.site_id && sites.value[0]) {
    agentForm.value.site_id = sites.value[0].site_id;
  }
}

async function loadAgents(): Promise<void> {
  const payload = await apiRequest<{ items: AdminAgent[] }>("/api/admin/v1/admin/agents?limit=100", { auth: true });
  agents.value = payload.items ?? [];
}

async function loadSiteAIConfig(siteId: string): Promise<void> {
  try {
    selectedAIConfig.value = await apiRequest<SiteAIConfig>(`/api/admin/v1/admin/sites/${encodeURIComponent(siteId)}/ai-config`, {
      auth: true,
    });
  } catch (error) {
    handleError(error, "加载站点 AI 配置失败");
  }
}

async function refreshAll(): Promise<void> {
  loading.value = true;
  try {
    await Promise.all([loadSites(), loadAgents()]);
    if (selectedSiteId.value) {
      await loadSiteAIConfig(selectedSiteId.value);
    }
    setStatus("数据已刷新");
  } catch (error) {
    handleError(error, "刷新数据失败");
  } finally {
    loading.value = false;
  }
}

async function createSite(): Promise<void> {
  savingSite.value = true;
  try {
    const domains = parseSiteDomains(siteForm.value.domains_text);
    if (isEditingSite.value) {
      await apiRequest(`/api/admin/v1/admin/sites/${encodeURIComponent(editingSiteId.value)}`, {
        method: "PATCH",
        auth: true,
        body: {
          name: siteForm.value.name,
          domains,
        },
      });
      await loadSites();
      selectedSiteId.value = editingSiteId.value;
      setStatus("站点已更新");
      return;
    }

    const createdSiteID = siteForm.value.site_id.trim();
    await apiRequest("/api/admin/v1/admin/sites", {
      method: "POST",
      auth: true,
      body: {
        site_id: siteForm.value.site_id,
        name: siteForm.value.name,
        domains,
      },
    });
    resetSiteForm();
    await loadSites();
    if (createdSiteID) {
      selectedSiteId.value = createdSiteID;
      agentForm.value.site_id = createdSiteID;
    }
    setStatus("站点创建成功");
  } catch (error) {
    handleError(error, isEditingSite.value ? "更新站点失败" : "创建站点失败");
  } finally {
    savingSite.value = false;
  }
}

async function createAgent(): Promise<void> {
  try {
    await apiRequest("/api/admin/v1/admin/agents", {
      method: "POST",
      auth: true,
      body: {
        ...agentForm.value,
        role: "agent",
      },
    });
    agentForm.value = {
      agent_id: "",
      site_id: sites.value[0]?.site_id ?? "",
      email: "",
      password: "",
      display_name: "",
    };
    await loadAgents();
    setStatus("坐席创建成功");
  } catch (error) {
    handleError(error, "创建坐席失败");
  }
}

async function saveAIConfig(): Promise<void> {
  if (!selectedSiteId.value) {
    setStatus("请先选择站点", true);
    return;
  }
  if (me.value?.role !== "super_admin") {
    setStatus("仅超级管理员可保存 AI 配置", true);
    return;
  }
  savingAI.value = true;
  try {
    selectedAIConfig.value = await apiRequest<SiteAIConfig>(
      `/api/admin/v1/admin/sites/${encodeURIComponent(selectedSiteId.value)}/ai-config`,
      {
        method: "PATCH",
        auth: true,
        body: {
          enabled: Boolean(selectedAIConfig.value?.enabled),
          reply_mode: selectedAIConfig.value?.reply_mode ?? "unassigned_auto_reply",
        },
      },
    );
    setStatus("AI 配置已保存");
  } catch (error) {
    handleError(error, "保存 AI 配置失败");
  } finally {
    savingAI.value = false;
  }
}

async function reloadAIKnowledge(): Promise<void> {
  if (!selectedSiteId.value) {
    setStatus("请先选择站点", true);
    return;
  }
  reloadingAI.value = true;
  try {
    const payload = await apiRequest<{ site_id: string; chunk_count: number; reloaded_at: string }>(
      `/api/admin/v1/admin/sites/${encodeURIComponent(selectedSiteId.value)}/ai/reload`,
      {
        method: "POST",
        auth: true,
      },
    );
    selectedAIConfig.value = {
      ...(selectedAIConfig.value ?? {
        site_id: selectedSiteId.value,
        enabled: false,
        reply_mode: "unassigned_auto_reply",
      }),
      chunk_count: payload.chunk_count,
      reloaded_at: payload.reloaded_at,
    };
    setStatus(`知识库已重载，分块 ${payload.chunk_count}`);
  } catch (error) {
    handleError(error, "重载知识库失败");
  } finally {
    reloadingAI.value = false;
  }
}

function toggleTheme(): void {
  theme.value = theme.value === "dark" ? "light" : "dark";
  applyTheme(theme.value);
}

function logout(): void {
  clearStaffToken();
  window.location.replace(`${LOGIN_PAGE_URL}?next=${encodeURIComponent(NEXT_URL)}`);
}

function setStatus(text: string, isError = false): void {
  statusText.value = text;
  statusError.value = isError;
}

function handleError(error: unknown, fallback: string): void {
  const message = error instanceof Error ? error.message : fallback;
  setStatus(message || fallback, true);
  if (error instanceof APIError && error.status === 401) {
    logout();
  }
}

function startSiteEdit(site: Site): void {
  editingSiteId.value = site.site_id;
  selectedSiteId.value = site.site_id;
  applySiteToForm(site);
  setStatus(`正在编辑站点 ${site.site_id}`);
}

function cancelSiteEdit(): void {
  resetSiteForm();
  setStatus("已取消站点编辑");
}

function applySiteToForm(site: Site): void {
  siteForm.value = {
    site_id: site.site_id,
    name: site.name,
    domains_text: site.domains.join(", "),
  };
}

function resetSiteForm(): void {
  editingSiteId.value = "";
  siteForm.value = {
    site_id: "",
    name: "",
    domains_text: "",
  };
}

function parseSiteDomains(raw: string): string[] {
  return Array.from(
    new Set(
      raw
        .split(/[\n,，;；]+/)
        .map((item) => item.trim().toLowerCase())
        .filter(Boolean),
    ),
  );
}

function formatSiteDomains(domains: string[]): string {
  if (!Array.isArray(domains) || domains.length === 0) {
    return "未绑定域名";
  }
  return domains.join("，");
}
</script>

<template>
  <div class="console-root">
    <div class="console-shell">
      <ConsoleHeader
        title="InlineChat 管理后台"
        subtitle="Vue3 版本默认接管管理控制台，站点、坐席与 AI 配置统一走响应式界面。"
        :user-text="userText"
        :role-text="roleText"
        :theme-text="themeText"
        :role-tone="roleTone"
        @toggle-theme="toggleTheme"
        @logout="logout"
      />

      <section class="metric-grid">
        <MetricCard label="站点总数" :value="sites.length" hint="基础资源池" />
        <MetricCard label="坐席总数" :value="agents.length" hint="统一由 admin-service 管控" />
        <MetricCard label="在线坐席" :value="activeAgentCount" hint="active 状态数量" />
        <MetricCard
          label="AI 当前站点"
          :value="selectedAIConfig?.enabled ? '已启用' : '未启用'"
          :hint="selectedSite ? `${selectedSite.site_id} · ${formatSiteDomains(selectedSite.domains)}` : '未选择站点'"
        />
      </section>

      <section class="workspace admin">
        <PanelCard title="站点中心" :description="sitePanelDescription">
          <template #actions>
            <div class="panel-actions">
              <button v-if="isEditingSite" class="ghost-button" type="button" @click="cancelSiteEdit">取消编辑</button>
              <button class="toolbar-button" type="button" @click="refreshAll">{{ loading ? "刷新中..." : "刷新" }}</button>
            </div>
          </template>

          <form id="createSiteForm" class="form-grid two-column" @submit.prevent="createSite">
            <label class="label-stack">
              <span>站点 ID</span>
              <input
                id="siteIdInput"
                v-model.trim="siteForm.site_id"
                class="text-field"
                :disabled="isEditingSite"
                placeholder="site_shop_main"
              />
            </label>
            <label class="label-stack">
              <span>站点名称</span>
              <input id="siteNameInput" v-model.trim="siteForm.name" class="text-field" placeholder="官方商城" />
            </label>
            <label class="label-stack">
              <span>绑定域名</span>
              <textarea
                id="siteDomainInput"
                v-model="siteForm.domains_text"
                class="text-field"
                rows="3"
                placeholder="shop.example.com, help.example.com"
              />
            </label>
            <div class="label-stack">
              <span>操作</span>
              <button class="primary-button" type="submit" :disabled="savingSite">{{ siteSubmitText }}</button>
            </div>
          </form>

          <div id="siteList" class="stack scroll-stack">
            <article
              v-for="site in sites"
              :key="site.site_id"
              class="list-card"
              :class="{ active: site.site_id === selectedSiteId || site.site_id === editingSiteId }"
            >
              <div class="panel-head">
                <div>
                  <h4>{{ site.name }}</h4>
                  <div class="meta-row">
                    <span>{{ site.site_id }}</span>
                    <span>{{ site.domains.length }} 个域名</span>
                    <span>{{ formatSiteDomains(site.domains) }}</span>
                    <span>{{ formatTime(site.updated_at) }}</span>
                  </div>
                </div>
                <div class="panel-actions">
                  <button class="ghost-button" type="button" @click="startSiteEdit(site)">编辑</button>
                  <button class="ghost-button" type="button" @click="selectedSiteId = site.site_id">查看 AI</button>
                </div>
              </div>
            </article>
            <div v-if="sites.length === 0" class="empty-state">暂无站点数据</div>
          </div>
        </PanelCard>

        <PanelCard title="坐席中心" description="表单、列表和状态操作后续可继续拆到更细的组件。">
          <form id="createAgentForm" class="form-grid two-column" @submit.prevent="createAgent">
            <label class="label-stack">
              <span>客服 ID</span>
              <input id="agentIdInput" v-model.trim="agentForm.agent_id" class="text-field" placeholder="0012" />
            </label>
            <label class="label-stack">
              <span>归属站点</span>
              <select id="agentSiteSelect" v-model="agentForm.site_id" class="select-field">
                <option value="">请选择站点</option>
                <option v-for="site in sites" :key="site.site_id" :value="site.site_id">
                  {{ site.site_id }} · {{ site.name }}
                </option>
              </select>
            </label>
            <label class="label-stack">
              <span>邮箱</span>
              <input id="agentEmailInput" v-model.trim="agentForm.email" class="text-field" placeholder="agent@example.com" />
            </label>
            <label class="label-stack">
              <span>密码</span>
              <input
                id="agentPasswordInput"
                v-model="agentForm.password"
                class="text-field"
                type="password"
                placeholder="12-72 位强密码"
              />
            </label>
            <label class="label-stack">
              <span>显示名</span>
              <input
                id="agentDisplayNameInput"
                v-model.trim="agentForm.display_name"
                class="text-field"
                placeholder="客服小青"
              />
            </label>
            <div class="label-stack">
              <span>操作</span>
              <button class="primary-button" type="submit">创建坐席</button>
            </div>
          </form>

          <div id="agentList" class="stack scroll-stack">
            <article v-for="agent in agents" :key="agent.id" class="list-card">
              <div class="panel-head">
                <div>
                  <h4>{{ agent.display_name }}</h4>
                  <div class="meta-row">
                    <span>{{ formatAgentID(agent.id) }}</span>
                    <span>{{ agent.email }}</span>
                    <span>{{ agent.site_id }}</span>
                  </div>
                </div>
                <StatusPill :text="agent.status" :tone="agent.status === 'active' ? 'success' : 'warn'" />
              </div>
            </article>
            <div v-if="agents.length === 0" class="empty-state">暂无坐席数据</div>
          </div>
        </PanelCard>

        <PanelCard title="AI 控制台" description="先接通现有接口，把站点级 AI 配置从手写 DOM 迁到响应式状态。">
          <div class="form-grid">
            <label class="label-stack">
              <span>当前站点</span>
              <select v-model="selectedSiteId" class="select-field">
                <option value="">请选择站点</option>
                <option v-for="site in sites" :key="site.site_id" :value="site.site_id">
                  {{ site.site_id }} · {{ site.name }}
                </option>
              </select>
            </label>

            <div class="meta-row">
              <StatusPill
                :text="selectedAIConfig?.enabled ? 'AI 已启用' : 'AI 未启用'"
                :tone="selectedAIConfig?.enabled ? 'success' : 'default'"
              />
              <StatusPill :text="selectedAIConfig?.reply_mode ?? 'unassigned_auto_reply'" tone="brand" />
            </div>

            <label class="label-stack">
              <span>自动回复开关</span>
              <select v-if="selectedAIConfig" v-model="selectedAIConfig.enabled" class="select-field">
                <option :value="true">启用</option>
                <option :value="false">关闭</option>
              </select>
              <input v-else class="text-field" value="请先选择站点" disabled />
            </label>

            <label class="label-stack">
              <span>回复模式</span>
              <select v-if="selectedAIConfig" v-model="selectedAIConfig.reply_mode" class="select-field">
                <option value="unassigned_auto_reply">仅未分配会话自动回复</option>
              </select>
              <input v-else class="text-field" value="请先选择站点" disabled />
            </label>

            <div class="meta-row" v-if="selectedAIConfig">
              <span>配置更新时间 {{ formatTime(selectedAIConfig.updated_at) }}</span>
              <span>最近重载 {{ formatTime(selectedAIConfig.reloaded_at) }}</span>
              <span>知识库分块 {{ selectedAIConfig.chunk_count ?? "--" }}</span>
            </div>

            <div class="panel-actions">
              <button class="primary-button" type="button" :disabled="savingAI || !selectedAIConfig" @click="saveAIConfig">
                {{ savingAI ? "保存中..." : "保存 AI 配置" }}
              </button>
              <button class="ghost-button" type="button" :disabled="reloadingAI || !selectedAIConfig" @click="reloadAIKnowledge">
                {{ reloadingAI ? "重载中..." : "重载知识库" }}
              </button>
            </div>
          </div>
        </PanelCard>
      </section>

      <footer id="statusLine" class="status-line" :class="{ error: statusError }">{{ statusText }}</footer>
    </div>
  </div>
</template>
