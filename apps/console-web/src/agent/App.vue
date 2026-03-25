<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, shallowRef } from "vue";

import ConsoleHeader from "../shared/components/ConsoleHeader.vue";
import MetricCard from "../shared/components/MetricCard.vue";
import PanelCard from "../shared/components/PanelCard.vue";
import StatusPill from "../shared/components/StatusPill.vue";
import { clearStaffToken, readStaffToken } from "../shared/auth";
import { apiRequest, APIError } from "../shared/api";
import {
  countBy,
  countMessageChars,
  formatAgentID,
  formatDurationSince,
  formatMessageStatus,
  formatMessageTime,
  formatTime,
  isValidAgentID,
  normalizeAgentID,
  truncateText,
} from "../shared/format";
import { initTheme, applyTheme } from "../shared/theme";
import type { Conversation, MeResponse, Message } from "../shared/types";

type QueueMode = "all" | "open" | "closed";
type ReplyTone = "default" | "brand" | "success" | "warn" | "danger";
type PendingRecord = { timer: ReturnType<typeof setTimeout> };
type RefreshMessagesOptions = {
  conversationID?: string;
  force?: boolean;
  forceScrollBottom?: boolean;
};
type NormalizedMessage = Message & {
  id: number;
  client_msg_id: string;
  sender_id: string;
  status: string;
};

interface AIIntentRule {
  key: string;
  label: string;
  detail: string;
  pattern: RegExp;
}

interface AIIntent {
  key: string;
  label: string;
  detail: string;
}

interface AIAction {
  badgeText: string;
  badgeTone: ReplyTone;
  title: string;
  detail: string;
}

interface AIInsight {
  badgeText: string;
  badgeTone: ReplyTone;
  intentTitle: string;
  intentDetail: string;
  actionTitle: string;
  actionDetail: string;
  summary: string;
  lastAIReplyText: string;
  suggestions: string[];
}

const DEFAULT_QUICK_REPLIES = [
  "您好，我来协助您处理这个问题。",
  "请稍等，我正在为您核实订单信息。",
  "已收到您的反馈，我们会尽快处理。",
  "如果方便，请提供一下订单号或手机号。",
  "感谢您的耐心等待，还有其他问题我也可以继续协助。",
];

const STORAGE_KEYS = {
  quickRepliesPrefix: "inlinechat.agent.quick_replies.",
  readCursorPrefix: "inlinechat.agent.read_cursor.",
};

const AI_INTENT_RULES: AIIntentRule[] = [
  {
    key: "complaint",
    label: "投诉 / 情绪安抚",
    detail: "访客情绪较强，先致歉稳住预期，再收集订单与问题经过。",
    pattern: /(投诉|差评|生气|不满意|失望|太慢|一直没人|马上|尽快|着急|催一下|催单|扯皮)/i,
  },
  {
    key: "refund",
    label: "退款 / 售后",
    detail: "优先确认订单号、退款原因与申请时间，避免往返追问。",
    pattern: /(退款|退货|换货|售后|取消订单|取消掉|退款进度|退回去)/i,
  },
  {
    key: "logistics",
    label: "订单 / 物流",
    detail: "先拿订单号或手机号，再同步发货、揽收或配送进度。",
    pattern: /(订单|物流|快递|发货|配送|到货|什么时候到|催发|查一下单|运单)/i,
  },
  {
    key: "recommendation",
    label: "选购 / 推荐",
    detail: "追问预算、使用场景和偏好，再给出更准确的推荐。",
    pattern: /(推荐|哪个好|怎么选|区别|适合我|型号|规格|尺码|颜色|搭配)/i,
  },
  {
    key: "price",
    label: "价格 / 活动",
    detail: "说明以页面实时活动为准，同时帮访客核对优惠与使用门槛。",
    pattern: /(多少钱|价格|优惠|折扣|活动|券|满减|便宜一点|促销)/i,
  },
  {
    key: "account",
    label: "账号 / 登录",
    detail: "让访客补充报错文案、操作步骤和账号信息，便于快速排查。",
    pattern: /(登录|账号|验证码|注册|密码|收不到|绑定|解绑)/i,
  },
  {
    key: "payment",
    label: "支付 / 发票",
    detail: "先确认订单号、支付时间和支付方式，再定位支付或开票问题。",
    pattern: /(支付|付款|扣款|发票|开票|支付失败|支付成功)/i,
  },
  {
    key: "manual",
    label: "人工诉求",
    detail: "访客明确希望人工介入，优先认领并说明你已接手。",
    pattern: /(人工|真人|客服|转人工|有人吗)/i,
  },
];

const LOGIN_PAGE_URL = "/app/staff-login/";
const NEXT_URL = "/app/agent/";
const ACK_TIMEOUT_MS = 5000;
const MAX_MESSAGE_CONTENT_CHARS = 2000;
const BASE_PAGE_TITLE = document.title || "InlineChat 客服工作台";

const theme = ref<"light" | "dark">(initTheme());
const token = ref(readStaffToken());
const me = ref<MeResponse | null>(null);
const loading = ref(false);
const sending = ref(false);
const statusText = ref("正在加载客服工作台...");
const statusError = ref(false);

const queueMode = ref<QueueMode>("all");
const conversationSearch = ref("");
const siteFilter = ref("");
const unassignedOnly = ref(false);
const mineOnly = ref(false);

const conversations = ref<Conversation[]>([]);
const statsConversations = ref<Conversation[]>([]);
const activeConversationId = ref("");
const activeConversation = ref<Conversation | null>(null);
const selectionSeq = ref(0);

const messages = ref<NormalizedMessage[]>([]);
const unreadMap = ref<Record<string, number>>({});
const readCursor = ref<Record<string, number>>({});
const quickReplies = ref<string[]>([...DEFAULT_QUICK_REPLIES]);
const quickReplyInput = ref("");
const draftMessage = ref("");
const transferAgentId = ref("");

const messageAbortController = shallowRef<AbortController | null>(null);
const messageListRef = ref<HTMLElement | null>(null);
const composerRef = ref<HTMLTextAreaElement | null>(null);
const ws = shallowRef<WebSocket | null>(null);
const wsConversationId = ref("");
const wsConnected = ref(false);
const wsReconnectPending = ref(false);
const pendingTransferItems = ref<Conversation[]>([]);
const pendingTransferConversationIDs = ref<string[]>([]);
const transferReminderInitialized = ref(false);

const pendingMap = reactive<Record<string, PendingRecord>>({});
const actionPending = reactive({
  select: false,
  claim: false,
  transfer: false,
  confirmTransfer: false,
  rejectTransfer: false,
  close: false,
  send: false,
});

let wsReconnectTimer: ReturnType<typeof setTimeout> | null = null;
let wsReconnectAttempt = 0;
let conversationTimer: ReturnType<typeof setTimeout> | null = null;
let messageTimer: ReturnType<typeof setTimeout> | null = null;
let conversationRefreshPending = false;
let conversationRefreshInFlight = false;
let messageResyncInFlight = false;
let messageResyncAttempt = 0;

const themeText = computed(() => (theme.value === "dark" ? "亮色模式" : "暗色模式"));
const userText = computed(() => (me.value ? `${me.value.email}` : "未登录"));
const agentBriefText = computed(() => {
  if (!me.value) {
    return "客服ID - · 角色 -";
  }
  const siteID = String(me.value.site_id || "-").trim() || "-";
  return `客服ID ${formatAgentID(me.value.agent_id)} · 角色 ${me.value.role} · site ${siteID}`;
});
const wsStateText = computed(() => {
  if (!activeConversationId.value) {
    return "实时通道：未绑定会话";
  }
  if (activeConversation.value?.status === "closed") {
    return `实时通道：会话已关闭 #${activeConversationId.value}`;
  }
  if (wsConnected.value && wsConversationId.value === activeConversationId.value) {
    return `实时通道：已连接 #${activeConversationId.value}`;
  }
  if (wsReconnectPending.value) {
    return `实时通道：重连中 #${activeConversationId.value}`;
  }
  return `实时通道：未连接 #${activeConversationId.value}`;
});
const wsTone = computed<ReplyTone>(() => {
  if (wsConnected.value && wsConversationId.value === activeConversationId.value) {
    return "success";
  }
  return wsReconnectPending.value ? "warn" : "default";
});

const filteredConversations = computed(() => {
  const keyword = conversationSearch.value.trim().toLowerCase();
  if (!keyword) {
    return conversations.value;
  }
  return conversations.value.filter((item) => {
    const parts = [
      String(item.id),
      item.site_id,
      item.assigned_agent_id ? formatAgentID(item.assigned_agent_id) : "",
    ];
    return parts.some((part) => part.toLowerCase().includes(keyword));
  });
});

const queueTotalCount = computed(() => statsConversations.value.length);
const queueOpenCount = computed(() => countBy(statsConversations.value, (item) => item.status === "open"));
const queueClosedCount = computed(() => countBy(statsConversations.value, (item) => item.status === "closed"));
const queueWaitingCount = computed(() =>
  countBy(statsConversations.value, (item) => item.status === "open" && !item.assigned_agent_id),
);
const queueUnassignedCount = computed(() => countBy(statsConversations.value, (item) => !item.assigned_agent_id));
const queueMineCount = computed(() =>
  countBy(statsConversations.value, (item) => item.status === "open" && item.assigned_agent_id === me.value?.agent_id),
);
const unreadTotalCount = computed(() =>
  Object.values(unreadMap.value).reduce((sum, value) => sum + Number(value || 0), 0),
);

const activeCapability = computed(() => {
  const conversation = activeConversation.value;
  const hasConversation =
    Boolean(token.value && me.value && activeConversationId.value && conversation) &&
    String(conversation?.id || "") === activeConversationId.value;
  const isOpen = conversation?.status === "open";
  const assignedAgentID = Number(conversation?.assigned_agent_id || 0);
  const meID = Number(me.value?.agent_id || 0);
  const pendingTransferToAgentID = Number(conversation?.pending_transfer_to_agent_id || 0);
  const isMine = meID > 0 && assignedAgentID > 0 && assignedAgentID === meID;
  const isUnassigned = assignedAgentID <= 0;
  const hasPendingTransfer = pendingTransferToAgentID > 0;
  const isPendingTransferTarget = meID > 0 && pendingTransferToAgentID === meID;

  return {
    hasConversation,
    isOpen,
    isMine,
    isUnassigned,
    hasPendingTransfer,
    isPendingTransferTarget,
  };
});

const canClaim = computed(
  () =>
    activeCapability.value.hasConversation &&
    activeCapability.value.isOpen &&
    activeCapability.value.isUnassigned &&
    !actionPending.select &&
    !actionPending.claim,
);
const canTransfer = computed(
  () =>
    activeCapability.value.hasConversation &&
    activeCapability.value.isOpen &&
    activeCapability.value.isMine &&
    !activeCapability.value.hasPendingTransfer &&
    !actionPending.select &&
    !actionPending.transfer,
);
const canConfirmTransfer = computed(
  () =>
    activeCapability.value.hasConversation &&
    activeCapability.value.isOpen &&
    activeCapability.value.isPendingTransferTarget &&
    !activeCapability.value.isMine &&
    !actionPending.select &&
    !actionPending.confirmTransfer,
);
const canRejectTransfer = computed(
  () =>
    activeCapability.value.hasConversation &&
    activeCapability.value.isOpen &&
    activeCapability.value.isPendingTransferTarget &&
    !activeCapability.value.isMine &&
    !actionPending.select &&
    !actionPending.rejectTransfer,
);
const showConfirmTransfer = computed(
  () =>
    activeCapability.value.hasConversation &&
    activeCapability.value.isOpen &&
    activeCapability.value.isPendingTransferTarget &&
    !activeCapability.value.isMine,
);
const canClose = computed(
  () =>
    activeCapability.value.hasConversation &&
    activeCapability.value.isOpen &&
    activeCapability.value.isMine &&
    !actionPending.select &&
    !actionPending.close,
);
const canSend = computed(
  () =>
    activeCapability.value.hasConversation &&
    activeCapability.value.isOpen &&
    activeCapability.value.isMine &&
    !actionPending.select &&
    !actionPending.send &&
    draftMessage.value.trim().length > 0,
);

const aiInsight = computed<AIInsight>(() => {
  if (!activeConversation.value) {
    return {
      badgeText: "未分析",
      badgeTone: "default",
      intentTitle: "请选择会话",
      intentDetail: "AI 会根据最近消息提炼主要诉求。",
      actionTitle: "等待会话",
      actionDetail: "认领后可直接填入建议回复。",
      summary: "请选择会话后查看 AI 摘要。",
      lastAIReplyText: "暂无 AI 回复",
      suggestions: [],
    };
  }
  return buildAIAssistInsight(activeConversation.value, messages.value, quickReplies.value, Number(me.value?.agent_id || 0));
});

onMounted(async () => {
  await bootstrap();
});

onBeforeUnmount(() => {
  resetPendingMap();
  closeWebSocket();
  stopPolling();
  abortMessageRequest();
  document.title = BASE_PAGE_TITLE;
});

async function bootstrap(): Promise<void> {
  loading.value = true;
  try {
    token.value = readStaffToken();
    me.value = await apiRequest<MeResponse>("/api/auth/v1/auth/me", { auth: true });
    if (me.value.role !== "agent") {
      throw new APIError("当前账号不是客服角色", 403);
    }
    siteFilter.value = String(me.value.site_id || "").trim();
    readCursor.value = loadReadCursor();
    quickReplies.value = loadQuickReplies();
    await refreshConversations();
    if (!activeConversationId.value && conversations.value[0]) {
      await selectConversation(conversations.value[0]);
    }
    startPolling();
    setStatus("客服工作台已就绪");
  } catch (error) {
    handleError(error, "加载客服台失败");
  } finally {
    loading.value = false;
  }
}

function toggleTheme(): void {
  theme.value = theme.value === "dark" ? "light" : "dark";
  applyTheme(theme.value);
  setStatus(`已切换为${theme.value === "dark" ? "暗色" : "亮色"}模式`);
}

function logout(): void {
  clearStaffToken();
  token.value = "";
  closeWebSocket();
  stopPolling();
  abortMessageRequest();
  document.title = BASE_PAGE_TITLE;
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

function setQueueMode(mode: QueueMode): void {
  if (queueMode.value === mode) {
    return;
  }
  queueMode.value = mode;
  void refreshConversations();
}

function handleUnassignedOnlyChange(): void {
  if (unassignedOnly.value) {
    mineOnly.value = false;
  }
  void refreshConversations();
}

function handleMineOnlyChange(): void {
  if (mineOnly.value) {
    unassignedOnly.value = false;
  }
  void refreshConversations();
}

async function refreshAll(): Promise<void> {
  loading.value = true;
  try {
    await refreshConversations();
    if (activeConversationId.value) {
      await refreshMessages({ force: true, forceScrollBottom: true });
    }
    setStatus("会话数据已刷新");
  } catch (error) {
    handleError(error, "刷新会话失败");
  } finally {
    loading.value = false;
  }
}

async function refreshConversations(): Promise<void> {
  if (!token.value) {
    return;
  }

  const search = new URLSearchParams();
  search.set("limit", "120");
  if (queueMode.value !== "all") {
    search.set("status", queueMode.value);
  }
  if (siteFilter.value) {
    search.set("site_id", siteFilter.value);
  }
  if (unassignedOnly.value) {
    search.set("unassigned_only", "true");
  } else if (mineOnly.value && me.value?.agent_id) {
    search.set("assigned_agent_id", String(me.value.agent_id));
  }

  const [queueData, statsData] = await Promise.all([
    apiRequest<{ items: Conversation[] }>(`/api/chat/v1/conversations?${search.toString()}`, { auth: true }),
    apiRequest<{ items: Conversation[] }>("/api/chat/v1/conversations?limit=200", { auth: true }),
  ]);

  conversations.value = sortConversations(queueData.items ?? []);
  statsConversations.value = sortConversations(statsData.items ?? []);
  syncTransferReminders(statsConversations.value);

  if (activeConversationId.value) {
    const found = conversations.value.find((item) => String(item.id) === activeConversationId.value);
    if (found) {
      activeConversation.value = found;
    } else {
      clearActiveConversation();
    }
  }

  await refreshUnreadCounts(statsConversations.value);
}

async function refreshUnreadCounts(items: Conversation[]): Promise<void> {
  if (!token.value) {
    return;
  }

  const openItems = items.filter((item) => item.status === "open").slice(0, 24);
  const nextUnreadMap: Record<string, number> = {};

  await Promise.all(
    openItems.map(async (item) => {
      const conversationID = String(item.id);
      try {
        const data = await apiRequest<{ items: Message[] }>(
          `/api/chat/v1/conversations/${conversationID}/messages?limit=80`,
          { auth: true },
        );
        const cursor = Number(readCursor.value[conversationID] || 0);
        const unread = (data.items ?? []).reduce((sum, message) => {
          const id = Number(message.id || 0);
          if (message.sender_type === "visitor" && id > cursor) {
            return sum + 1;
          }
          return sum;
        }, 0);
        nextUnreadMap[conversationID] = unread;
      } catch {
        nextUnreadMap[conversationID] = Number(unreadMap.value[conversationID] || 0);
      }
    }),
  );

  if (activeConversationId.value && canAgentMarkConversationRead(activeConversationId.value)) {
    nextUnreadMap[activeConversationId.value] = 0;
  }
  unreadMap.value = nextUnreadMap;
}

async function selectConversation(conversation: Conversation): Promise<void> {
  const conversationID = String(conversation?.id || "").trim();
  if (!conversationID) {
    return;
  }

  const currentSelectionSeq = selectionSeq.value + 1;
  selectionSeq.value = currentSelectionSeq;
  actionPending.select = true;

  activeConversationId.value = conversationID;
  activeConversation.value = conversation;
  transferAgentId.value = "";
  resetPendingMap();
  messages.value = [];
  abortMessageRequest();
  clearWsReconnectTimer();
  wsReconnectTimer = null;
  closeWebSocket();
  await scrollMessageListToBottom(true);

  try {
    await refreshMessages({
      conversationID,
      force: true,
      forceScrollBottom: true,
    });
    if (currentSelectionSeq !== selectionSeq.value || conversationID !== activeConversationId.value) {
      return;
    }
    if (activeConversation.value?.status === "open") {
      connectWebSocket();
      scheduleMessageResync(300);
      setStatus(`已切换到会话 #${conversation.id}`);
    } else {
      setStatus(`已切换到已关闭会话 #${conversation.id}`);
    }
  } catch (error) {
    if (currentSelectionSeq === selectionSeq.value && conversationID === activeConversationId.value) {
      handleError(error, "消息加载失败");
    }
  } finally {
    if (currentSelectionSeq === selectionSeq.value) {
      actionPending.select = false;
    }
  }
}

function clearActiveConversation(): void {
  activeConversationId.value = "";
  activeConversation.value = null;
  messages.value = [];
  transferAgentId.value = "";
  resetPendingMap();
  abortMessageRequest();
  closeWebSocket();
}

async function refreshMessages(options: RefreshMessagesOptions = {}): Promise<void> {
  const conversationID = String(options.conversationID || activeConversationId.value || "").trim();
  if (!conversationID) {
    return;
  }

  if (messageAbortController.value && !options.force) {
    return;
  }
  if (options.force) {
    abortMessageRequest();
  }

  const controller = new AbortController();
  messageAbortController.value = controller;

  try {
    const data = await apiRequest<{ items: Message[] }>(
      `/api/chat/v1/conversations/${conversationID}/messages?limit=200`,
      {
        auth: true,
        signal: controller.signal,
      },
    );
    if (messageAbortController.value !== controller) {
      return;
    }
    if (conversationID !== activeConversationId.value) {
      return;
    }
    mergeMessages(data.items ?? [], {
      forceScrollBottom: Boolean(options.forceScrollBottom),
    });
  } catch (error) {
    if (isAbortError(error)) {
      return;
    }
    throw error;
  } finally {
    if (messageAbortController.value === controller) {
      messageAbortController.value = null;
    }
  }
}

function abortMessageRequest(): void {
  if (!messageAbortController.value) {
    return;
  }
  messageAbortController.value.abort();
  messageAbortController.value = null;
}

function mergeMessages(items: Message[], options: { forceScrollBottom?: boolean } = {}): void {
  const byKey = new Map<string, NormalizedMessage>();
  const clientMsgKey = new Map<string, string>();
  const shouldStickBottom = Boolean(options.forceScrollBottom) || isNearBottom(messageListRef.value);

  for (const current of messages.value) {
    const normalized = normalizeMessage(current);
    if (!normalized) {
      continue;
    }
    const key = getMessageKey(normalized);
    byKey.set(key, normalized);
    if (normalized.client_msg_id) {
      clientMsgKey.set(normalized.client_msg_id, key);
    }
  }

  for (const incomingRaw of items) {
    const incoming = normalizeMessage(incomingRaw);
    if (!incoming) {
      continue;
    }

    const clientMsgID = incoming.client_msg_id;
    const incomingKey = getMessageKey(incoming);
    let hitKey = "";
    if (clientMsgID && clientMsgKey.has(clientMsgID)) {
      hitKey = clientMsgKey.get(clientMsgID) ?? "";
    } else if (byKey.has(incomingKey)) {
      hitKey = incomingKey;
    }

    if (!hitKey) {
      byKey.set(incomingKey, incoming);
      if (clientMsgID) {
        clientMsgKey.set(clientMsgID, incomingKey);
      }
    } else {
      const merged = mergeMessageRecord(byKey.get(hitKey), incoming);
      const mergedKey = getMessageKey(merged);
      if (mergedKey !== hitKey) {
        byKey.delete(hitKey);
      }
      byKey.set(mergedKey, merged);
      if (merged.client_msg_id) {
        clientMsgKey.set(merged.client_msg_id, mergedKey);
      }
    }

    if (clientMsgID && Number(incoming.id || 0) > 0) {
      clearPending(clientMsgID);
    }
  }

  messages.value = Array.from(byKey.values()).sort(compareMessageOrder);
  if (activeConversationId.value) {
    markConversationRead(activeConversationId.value, messages.value);
  }
  void scrollMessageListToBottom(shouldStickBottom);
}

function normalizeMessage(message: Message | NormalizedMessage | null | undefined): NormalizedMessage | null {
  if (!message || typeof message !== "object") {
    return null;
  }
  const id = Number(message.id || 0);
  const clientMsgID = String(message.client_msg_id || "").trim();
  if (id <= 0 && !clientMsgID) {
    return null;
  }
  return {
    ...message,
    id: id > 0 ? id : 0,
    client_msg_id: clientMsgID,
    sender_type: String(message.sender_type || "").trim().toLowerCase() as NormalizedMessage["sender_type"],
    sender_id: String(message.sender_id || "").trim(),
    status: normalizeMessageStatus(message.status),
    created_at: message.created_at || new Date().toISOString(),
    updated_at: message.updated_at || message.created_at || new Date().toISOString(),
  };
}

function normalizeMessageStatus(status: string | undefined): string {
  const normalized = String(status || "")
    .trim()
    .toLowerCase();
  if (normalized === "sending" || normalized === "sent" || normalized === "read" || normalized === "failed") {
    return normalized;
  }
  return "";
}

function getMessageKey(message: NormalizedMessage): string {
  if (Number(message.id || 0) > 0) {
    return `id:${message.id}`;
  }
  return `client:${message.client_msg_id}`;
}

function mergeMessageRecord(current: NormalizedMessage | undefined, incoming: NormalizedMessage): NormalizedMessage {
  const merged: NormalizedMessage = {
    ...(current ?? incoming),
    ...incoming,
  };
  if (!incoming.status && current?.status) {
    merged.status = current.status;
  }
  return merged;
}

function compareMessageOrder(a: NormalizedMessage, b: NormalizedMessage): number {
  const idA = Number(a.id || 0);
  const idB = Number(b.id || 0);
  if (idA > 0 && idB > 0 && idA !== idB) {
    return idA - idB;
  }
  if (idA > 0 && idB <= 0) {
    return -1;
  }
  if (idA <= 0 && idB > 0) {
    return 1;
  }
  const timeA = Date.parse(a.created_at || "");
  const timeB = Date.parse(b.created_at || "");
  if (!Number.isNaN(timeA) && !Number.isNaN(timeB) && timeA !== timeB) {
    return timeA - timeB;
  }
  return 0;
}

function createLocalOutgoingMessage(content: string, clientMsgID: string): NormalizedMessage {
  const now = new Date().toISOString();
  return {
    id: 0,
    conversation_id: Number(activeConversationId.value || 0),
    sender_type: "agent",
    sender_id: String(me.value?.agent_id || ""),
    content,
    client_msg_id: clientMsgID,
    status: "sending",
    created_at: now,
    updated_at: now,
  };
}

function isMineMessage(message: NormalizedMessage): boolean {
  return message.sender_type === "agent" && String(message.sender_id || "") === String(me.value?.agent_id || "");
}

function canRetryMessage(message: NormalizedMessage): boolean {
  return isMineMessage(message) && message.status === "failed" && Boolean(message.client_msg_id);
}

function formatMessageSenderName(message: NormalizedMessage): string {
  if (message.sender_type === "system") {
    return "系统";
  }
  if (message.sender_type === "ai") {
    return "AI顾问";
  }
  if (message.sender_type === "visitor") {
    return "访客";
  }
  if (isMineMessage(message)) {
    return `客服 ${formatAgentID(me.value?.agent_id)}`;
  }
  if (message.sender_type === "agent") {
    return `客服 ${formatAgentID(message.sender_id)}`;
  }
  return "未知发送方";
}

function formatMessageFooterText(message: NormalizedMessage): string {
  const timeText = formatMessageTime(message.created_at);
  if (!isMineMessage(message)) {
    return timeText;
  }
  const statusText = formatMessageStatus(message.status);
  return [timeText, statusText].filter(Boolean).join(" · ");
}

function isNearBottom(container: HTMLElement | null, threshold = 72): boolean {
  if (!container) {
    return true;
  }
  const gap = container.scrollHeight - container.scrollTop - container.clientHeight;
  return gap <= threshold;
}

async function scrollMessageListToBottom(force = false): Promise<void> {
  await nextTick();
  const container = messageListRef.value;
  if (!container) {
    return;
  }
  if (!force && !isNearBottom(container)) {
    return;
  }
  container.scrollTop = container.scrollHeight;
}

function beginPending(clientMsgID: string): void {
  clearPending(clientMsgID, true);
  const timer = setTimeout(() => {
    clearPending(clientMsgID, true);
    markMessageFailedByClientMsgID(clientMsgID);
    setStatus("消息发送超时，请重发", true);
  }, ACK_TIMEOUT_MS);
  pendingMap[clientMsgID] = { timer };
}

function clearPending(clientMsgID: string, silent = false): void {
  const pending = pendingMap[clientMsgID];
  if (pending?.timer) {
    clearTimeout(pending.timer);
  }
  delete pendingMap[clientMsgID];
  if (!silent) {
    syncSendingStatusText();
  }
}

function resetPendingMap(): void {
  for (const clientMsgID of Object.keys(pendingMap)) {
    clearPending(clientMsgID, true);
  }
}

function syncSendingStatusText(): void {
  if (Object.keys(pendingMap).length > 0) {
    return;
  }
  if (statusText.value === "消息发送中..." || statusText.value === "消息重发中...") {
    setStatus("消息已发送");
  }
}

function updateMessageByClientMsgID(clientMsgID: string, patch: Partial<NormalizedMessage>): boolean {
  const key = String(clientMsgID || "").trim();
  if (!key) {
    return false;
  }
  let changed = false;
  messages.value = messages.value.map((item) => {
    if (item.client_msg_id !== key) {
      return item;
    }
    const next = {
      ...item,
      ...patch,
      status: patch.status ? normalizeMessageStatus(patch.status) || item.status : item.status,
      sender_id: patch.sender_id !== undefined ? String(patch.sender_id || "") : item.sender_id,
    };
    if (
      next.id !== item.id ||
      next.status !== item.status ||
      next.updated_at !== item.updated_at ||
      next.sender_id !== item.sender_id
    ) {
      changed = true;
    }
    return next;
  });
  if (changed) {
    messages.value = [...messages.value].sort(compareMessageOrder);
  }
  return changed;
}

function updateMessageByID(messageID: number, patch: Partial<NormalizedMessage>): boolean {
  const id = Number(messageID || 0);
  if (id <= 0) {
    return false;
  }
  let changed = false;
  messages.value = messages.value.map((item) => {
    if (Number(item.id || 0) !== id) {
      return item;
    }
    const next = {
      ...item,
      ...patch,
      status: patch.status ? normalizeMessageStatus(patch.status) || item.status : item.status,
    };
    if (next.status !== item.status || next.updated_at !== item.updated_at) {
      changed = true;
    }
    return next;
  });
  return changed;
}

function markMessageFailedByClientMsgID(clientMsgID: string): boolean {
  return updateMessageByClientMsgID(clientMsgID, {
    status: "failed",
    updated_at: new Date().toISOString(),
  });
}

function markAllSendingMessagesFailed(): void {
  messages.value = messages.value.map((item) => {
    if (item.status !== "sending") {
      return item;
    }
    return {
      ...item,
      status: "failed",
      updated_at: new Date().toISOString(),
    };
  });
  messages.value = [...messages.value];
}

function handleMessageAck(payload: Record<string, unknown>): void {
  const clientMsgID = String(payload.client_msg_id || "").trim();
  if (!clientMsgID) {
    return;
  }
  clearPending(clientMsgID);
  updateMessageByClientMsgID(clientMsgID, {
    id: Number(payload.message_id || 0),
    status: normalizeMessageStatus(String(payload.status || "")) || "sent",
    updated_at: new Date().toISOString(),
  });
}

function handleMessageNack(payload: Record<string, unknown>): void {
  const clientMsgID = String(payload.client_msg_id || "").trim();
  if (!clientMsgID) {
    return;
  }
  clearPending(clientMsgID);
  markMessageFailedByClientMsgID(clientMsgID);
  setStatus(extractErrorMessage(payload.error, "发送失败"), true);
}

function handleMessageStatusEvent(payload: Record<string, unknown>): void {
  const conversationID = Number(payload.conversation_id || 0);
  if (conversationID > 0 && String(conversationID) !== activeConversationId.value) {
    return;
  }

  const status = normalizeMessageStatus(String(payload.status || ""));
  if (!status) {
    return;
  }

  const messageID = Number(payload.message_id || 0);
  if (messageID > 0) {
    updateMessageByID(messageID, {
      status,
      updated_at: new Date().toISOString(),
    });
    return;
  }

  const upToMessageID = Number(payload.up_to_message_id || 0);
  const senderType = String(payload.sender_type || "").trim().toLowerCase();
  if (status !== "read" || upToMessageID <= 0 || !senderType) {
    return;
  }

  messages.value = messages.value.map((item) => {
    if (item.sender_type !== senderType) {
      return item;
    }
    if (Number(item.id || 0) <= 0 || Number(item.id || 0) > upToMessageID || item.status === "read") {
      return item;
    }
    return {
      ...item,
      status: "read",
      updated_at: new Date().toISOString(),
    };
  });
}

function applyConversationSnapshotPatch(conversationID: string, patch: Partial<Conversation>): void {
  if (!conversationID) {
    return;
  }
  conversations.value = conversations.value.map((item) =>
    String(item.id) === conversationID ? { ...item, ...patch } : item,
  );
  statsConversations.value = statsConversations.value.map((item) =>
    String(item.id) === conversationID ? { ...item, ...patch } : item,
  );
  if (activeConversation.value && String(activeConversation.value.id) === conversationID) {
    activeConversation.value = {
      ...activeConversation.value,
      ...patch,
    };
  }
}

function handleConversationStatusEvent(payload: Record<string, unknown>, fallbackStatus = ""): void {
  const conversationID = String(payload.conversation_id || activeConversationId.value || "").trim();
  if (!conversationID) {
    return;
  }
  const status = String(payload.status || fallbackStatus || "")
    .trim()
    .toLowerCase();
  if (status !== "open" && status !== "closed") {
    return;
  }

  applyConversationSnapshotPatch(conversationID, {
    status: status as Conversation["status"],
    updated_at: new Date().toISOString(),
  });

  if (conversationID === activeConversationId.value && status === "closed") {
    resetPendingMap();
    markAllSendingMessagesFailed();
    closeWebSocket();
    setStatus("会话已关闭，无法继续发送消息。");
  }
  scheduleConversationRefresh(0);
}

function isWebSocketReady(): boolean {
  return Boolean(
    ws.value &&
      ws.value.readyState === WebSocket.OPEN &&
      wsConnected.value &&
      wsConversationId.value === activeConversationId.value,
  );
}

function sendMessageViaWS(payload: Record<string, unknown>): void {
  if (!ws.value || ws.value.readyState !== WebSocket.OPEN) {
    throw new Error("实时通道未连接，请重连后重发");
  }
  beginPending(String(payload.client_msg_id || ""));
  try {
    ws.value.send(
      JSON.stringify({
        type: "message.send",
        payload,
      }),
    );
  } catch (error) {
    clearPending(String(payload.client_msg_id || ""), true);
    throw error instanceof Error ? error : new Error("消息发送失败");
  }
}

async function sendMessageViaHTTP(payload: Record<string, unknown>): Promise<Message> {
  if (!activeConversationId.value) {
    throw new Error("会话状态未就绪，请稍后重试。");
  }
  return apiRequest<Message>(`/api/chat/v1/conversations/${activeConversationId.value}/messages`, {
    method: "POST",
    auth: true,
    body: payload,
  });
}

async function dispatchAgentMessage(payload: Record<string, unknown>): Promise<"ws" | "http"> {
  if (isWebSocketReady()) {
    try {
      sendMessageViaWS(payload);
      return "ws";
    } catch {
      // 发送瞬间实时通道不可用时，降级 HTTP。
    }
  }

  const created = await sendMessageViaHTTP(payload);
  updateMessageByClientMsgID(String(payload.client_msg_id || ""), {
    id: Number(created.id || 0),
    status: normalizeMessageStatus(created.status) || "sent",
    updated_at: created.updated_at || new Date().toISOString(),
    sender_id: String(created.sender_id || ""),
  });
  mergeMessages([created], { forceScrollBottom: true });
  scheduleConversationRefresh(300);
  return "http";
}

async function resendMessage(clientMsgID: string): Promise<void> {
  const message = messages.value.find((item) => item.client_msg_id === clientMsgID);
  if (!message || !isMineMessage(message)) {
    return;
  }

  try {
    updateMessageByClientMsgID(clientMsgID, {
      status: "sending",
      updated_at: new Date().toISOString(),
    });
    const mode = await dispatchAgentMessage({
      sender_type: "agent",
      content: message.content || "",
      client_msg_id: clientMsgID,
    });
    setStatus(mode === "http" ? "实时通道未连接，已通过 HTTP 重发" : "消息重发中...");
  } catch (error) {
    markMessageFailedByClientMsgID(clientMsgID);
    handleError(error, "重发失败");
  }
}

async function sendMessage(): Promise<void> {
  if (actionPending.send) {
    return;
  }
  if (!activeConversationId.value) {
    setStatus("请先选择会话", true);
    return;
  }
  if (!activeCapability.value.isOpen) {
    setStatus("会话已关闭，无法继续发送，请切换其他会话。", true);
    return;
  }
  if (!activeCapability.value.isMine) {
    setStatus("请先认领会话后再发送消息。", true);
    return;
  }
  if (!me.value?.agent_id) {
    setStatus("当前登录信息缺少 agent_id", true);
    return;
  }

  const content = draftMessage.value.trim();
  if (!content) {
    return;
  }
  if (countMessageChars(content) > MAX_MESSAGE_CONTENT_CHARS) {
    setStatus(`消息过长，最多 ${MAX_MESSAGE_CONTENT_CHARS} 个字符`, true);
    return;
  }

  const clientMsgID = `a_${safeUUID()}`;
  actionPending.send = true;
  sending.value = true;
  try {
    mergeMessages([createLocalOutgoingMessage(content, clientMsgID)], {
      forceScrollBottom: true,
    });
    const mode = await dispatchAgentMessage({
      sender_type: "agent",
      content,
      client_msg_id: clientMsgID,
    });
    draftMessage.value = "";
    await focusComposer();
    setStatus(mode === "http" ? "实时通道未连接，已通过 HTTP 发送" : "消息发送中...");
  } catch (error) {
    markMessageFailedByClientMsgID(clientMsgID);
    handleError(error, "发送失败");
  } finally {
    actionPending.send = false;
    sending.value = false;
  }
}

function handleComposerKeydown(event: KeyboardEvent): void {
  if (event.key !== "Enter" || event.shiftKey) {
    return;
  }
  event.preventDefault();
  if (!sending.value && canSend.value) {
    void sendMessage();
  }
}

async function focusComposer(): Promise<void> {
  await nextTick();
  composerRef.value?.focus();
}

function connectWebSocket(): void {
  if (!activeConversationId.value) {
    wsConnected.value = false;
    wsConversationId.value = "";
    return;
  }
  if (activeConversation.value?.status === "closed") {
    wsConnected.value = false;
    wsConversationId.value = activeConversationId.value;
    wsReconnectPending.value = false;
    return;
  }

  clearWsReconnectTimer();
  if (ws.value) {
    ws.value.close();
    ws.value = null;
  }

  const conversationID = activeConversationId.value;
  wsConversationId.value = conversationID;
  wsConnected.value = false;
  wsReconnectPending.value = false;

  const protocol = window.location.protocol === "https:" ? "wss" : "ws";
  const wsURL = `${protocol}://${window.location.host}/ws/${conversationID}?access_token=${encodeURIComponent(token.value)}`;
  const socket = new WebSocket(wsURL);

  socket.addEventListener("open", () => {
    if (ws.value !== socket) {
      return;
    }
    wsConnected.value = true;
    wsReconnectPending.value = false;
    wsReconnectAttempt = 0;
    messageResyncAttempt = 0;
    cancelMessageResync();
    setStatus(`WebSocket 已连接，会话 #${conversationID}`);
    scheduleConversationRefresh(400);
  });

  socket.addEventListener("message", (event) => {
    try {
      const data = JSON.parse(event.data) as {
        type?: string;
        payload?: Record<string, unknown> & { message?: Message };
        error?: unknown;
      };
      switch (data.type) {
        case "message.new":
          if (data.payload?.message) {
            const cid = String(data.payload.conversation_id || data.payload.message.conversation_id || "");
            if (!cid || cid !== activeConversationId.value) {
              return;
            }
            mergeMessages([data.payload.message], { forceScrollBottom: false });
            scheduleConversationRefresh(500);
          }
          break;
        case "message.ack":
          handleMessageAck(data.payload || {});
          break;
        case "message.nack":
          handleMessageNack(data.payload || {});
          break;
        case "message.status":
          handleMessageStatusEvent(data.payload || {});
          scheduleConversationRefresh(600);
          break;
        case "conversation.closed":
          handleConversationStatusEvent(data.payload || {}, "closed");
          break;
        case "conversation.status":
          handleConversationStatusEvent(data.payload || {});
          break;
        case "error":
          setStatus(extractErrorMessage(data.error, "WebSocket 消息异常"), true);
          break;
        default:
          break;
      }
    } catch {
      setStatus("收到无法解析的 WebSocket 消息", true);
    }
  });

  socket.addEventListener("close", () => {
    if (ws.value !== socket) {
      return;
    }
    ws.value = null;
    wsConnected.value = false;
    if (conversationID !== activeConversationId.value) {
      return;
    }
    if (activeConversation.value?.status === "closed") {
      return;
    }
    scheduleWsReconnect(conversationID);
    scheduleMessageResync(300);
    setStatus("WebSocket 已断开，正在自动重连", true);
  });

  socket.addEventListener("error", () => {
    if (ws.value !== socket) {
      return;
    }
    wsConnected.value = false;
    scheduleMessageResync(300);
    setStatus("WebSocket 异常，正在自动重连", true);
  });

  ws.value = socket;
}

function closeWebSocket(): void {
  clearWsReconnectTimer();
  if (ws.value) {
    ws.value.close();
    ws.value = null;
  }
  wsConnected.value = false;
  wsConversationId.value = "";
  wsReconnectPending.value = false;
  wsReconnectAttempt = 0;
  cancelMessageResync();
}

function clearWsReconnectTimer(): void {
  if (wsReconnectTimer) {
    clearTimeout(wsReconnectTimer);
    wsReconnectTimer = null;
  }
  wsReconnectPending.value = false;
}

function scheduleWsReconnect(conversationID: string): void {
  if (!conversationID || conversationID !== activeConversationId.value || wsReconnectTimer) {
    return;
  }
  if (activeConversation.value?.status === "closed") {
    return;
  }

  const baseDelay = Math.min(1000 * 2 ** wsReconnectAttempt, 10_000);
  const jitter = Math.floor(Math.random() * 250);
  const delay = baseDelay + jitter;
  wsReconnectAttempt += 1;
  wsReconnectPending.value = true;

  wsReconnectTimer = setTimeout(() => {
    wsReconnectTimer = null;
    wsReconnectPending.value = false;
    if (!token.value || !activeConversationId.value || activeConversationId.value !== conversationID) {
      return;
    }
    connectWebSocket();
  }, delay);
}

function startPolling(): void {
  stopPolling();
  scheduleMessageResync(0);
}

function stopPolling(): void {
  if (conversationTimer) {
    clearTimeout(conversationTimer);
    conversationTimer = null;
  }
  conversationRefreshPending = false;
  conversationRefreshInFlight = false;
  if (messageTimer) {
    clearTimeout(messageTimer);
    messageTimer = null;
  }
  messageResyncInFlight = false;
  messageResyncAttempt = 0;
}

function scheduleConversationRefresh(delayMs = 0): void {
  if (!token.value) {
    return;
  }
  if (conversationTimer) {
    conversationRefreshPending = true;
    return;
  }

  conversationTimer = setTimeout(async () => {
    conversationTimer = null;
    if (!token.value) {
      conversationRefreshPending = false;
      return;
    }
    if (conversationRefreshInFlight) {
      conversationRefreshPending = true;
      scheduleConversationRefresh(300);
      return;
    }

    conversationRefreshInFlight = true;
    try {
      await refreshConversations();
    } catch (error) {
      handleError(error, "会话刷新失败");
    } finally {
      conversationRefreshInFlight = false;
      if (conversationRefreshPending) {
        conversationRefreshPending = false;
        scheduleConversationRefresh(300);
      }
    }
  }, Math.max(0, Number(delayMs) || 0));
}

function shouldResyncMessages(): boolean {
  if (!token.value || !activeConversationId.value) {
    return false;
  }
  if (activeConversation.value?.status === "closed") {
    return false;
  }
  return !(wsConnected.value && wsConversationId.value === activeConversationId.value);
}

function computeMessageResyncDelay(): number {
  const base = Math.min(2500 * 2 ** messageResyncAttempt, 15_000);
  const jitter = Math.floor(Math.random() * 250);
  return base + jitter;
}

function scheduleMessageResync(delayMs?: number): void {
  if (!shouldResyncMessages() || messageTimer) {
    return;
  }
  const delay = Number.isFinite(Number(delayMs)) ? Math.max(0, Number(delayMs)) : computeMessageResyncDelay();

  messageTimer = setTimeout(async () => {
    messageTimer = null;
    if (!shouldResyncMessages()) {
      messageResyncAttempt = 0;
      return;
    }
    if (messageResyncInFlight) {
      scheduleMessageResync(300);
      return;
    }

    messageResyncInFlight = true;
    try {
      await refreshMessages();
      messageResyncAttempt = 0;
    } catch (error) {
      messageResyncAttempt = Math.min(messageResyncAttempt + 1, 6);
      handleError(error, "消息刷新失败");
    } finally {
      messageResyncInFlight = false;
      if (shouldResyncMessages()) {
        scheduleMessageResync();
      }
    }
  }, delay);
}

function cancelMessageResync(): void {
  if (messageTimer) {
    clearTimeout(messageTimer);
    messageTimer = null;
  }
  messageResyncInFlight = false;
  messageResyncAttempt = 0;
}

function canAgentMarkConversationRead(conversationID: string): boolean {
  if (!conversationID || !me.value?.agent_id) {
    return false;
  }
  const conversation =
    (activeConversation.value && String(activeConversation.value.id) === conversationID ? activeConversation.value : null) ||
    conversations.value.find((item) => String(item.id) === conversationID) ||
    statsConversations.value.find((item) => String(item.id) === conversationID) ||
    null;
  if (!conversation) {
    return false;
  }
  return Number(conversation.assigned_agent_id || 0) === Number(me.value.agent_id);
}

function markConversationRead(conversationID: string, currentMessages: NormalizedMessage[]): void {
  if (!conversationID || !canAgentMarkConversationRead(conversationID)) {
    return;
  }

  const maxMessageID = currentMessages.reduce((max, message) => Math.max(max, Number(message.id || 0)), 0);
  if (maxMessageID <= 0) {
    return;
  }

  const previous = Number(readCursor.value[conversationID] || 0);
  if (maxMessageID > previous) {
    readCursor.value = {
      ...readCursor.value,
      [conversationID]: maxMessageID,
    };
    saveReadCursor();
    void reportConversationRead(conversationID, maxMessageID);
  }

  if (Number(unreadMap.value[conversationID] || 0) !== 0) {
    unreadMap.value = {
      ...unreadMap.value,
      [conversationID]: 0,
    };
  }
}

async function reportConversationRead(conversationID: string, lastReadMessageID: number): Promise<void> {
  if (!conversationID || !lastReadMessageID || !canAgentMarkConversationRead(conversationID)) {
    return;
  }
  try {
    const response = await apiRequest<{ updated_count?: number }>(
      `/api/chat/v1/conversations/${conversationID}/read`,
      {
        method: "POST",
        auth: true,
        body: {
          last_read_message_id: lastReadMessageID,
        },
      },
    );
    if (Number(response.updated_count || 0) > 0 && conversationID === activeConversationId.value) {
      await refreshMessages({ conversationID, force: true });
    }
  } catch {
    // 仅做状态上报，不打断客服操作。
  }
}

function collectPendingTransferConversations(items: Conversation[]): Conversation[] {
  const meID = Number(me.value?.agent_id || 0);
  if (meID <= 0) {
    return [];
  }
  return [...items]
    .filter((item) => item.status === "open" && Number(item.pending_transfer_to_agent_id || 0) === meID)
    .sort((a, b) => Date.parse(b.updated_at || b.created_at) - Date.parse(a.updated_at || a.created_at));
}

function syncTransferReminders(items: Conversation[]): void {
  const nextPendingItems = collectPendingTransferConversations(items);
  const nextIDs = nextPendingItems.map((item) => String(item.id));
  const previousIDs = new Set(pendingTransferConversationIDs.value);
  const incomingNewIDs = nextIDs.filter((id) => !previousIDs.has(id));

  pendingTransferItems.value = nextPendingItems;
  pendingTransferConversationIDs.value = nextIDs;
  updateDocumentTitleByTransferReminderCount(nextPendingItems.length);

  if (transferReminderInitialized.value && incomingNewIDs.length > 0) {
    const total = nextPendingItems.length;
    setStatus(total > 1 ? `你有 ${total} 个待确认转接请求` : "你有新的待确认转接请求");
  }
  transferReminderInitialized.value = true;
}

function updateDocumentTitleByTransferReminderCount(count: number): void {
  document.title = count > 0 ? `[待确认转接 ${count}] ${BASE_PAGE_TITLE}` : BASE_PAGE_TITLE;
}

async function jumpToTransferReminderConversation(conversationID: string): Promise<void> {
  const id = String(conversationID || "").trim();
  if (!id) {
    return;
  }

  queueMode.value = "open";
  unassignedOnly.value = false;
  mineOnly.value = false;
  await refreshConversations();

  const conversation =
    conversations.value.find((item) => String(item.id) === id) ||
    statsConversations.value.find((item) => String(item.id) === id) ||
    null;
  if (!conversation) {
    setStatus("待确认转接会话不存在或已关闭", true);
    return;
  }
  await selectConversation(conversation);
}

async function claimConversation(): Promise<void> {
  if (actionPending.claim) {
    return;
  }
  if (!activeConversationId.value) {
    setStatus("请先选择会话", true);
    return;
  }

  actionPending.claim = true;
  try {
    await apiRequest(`/api/chat/v1/conversations/${activeConversationId.value}/claim`, {
      method: "POST",
      auth: true,
    });
    await refreshConversations();
    if (activeConversationId.value) {
      await refreshMessages({ conversationID: activeConversationId.value, force: true, forceScrollBottom: true });
      markConversationRead(activeConversationId.value, messages.value);
    }
    setStatus("认领成功");
  } catch (error) {
    handleError(error, "认领失败");
  } finally {
    actionPending.claim = false;
  }
}

async function transferConversation(): Promise<void> {
  if (actionPending.transfer) {
    return;
  }
  if (!activeConversationId.value) {
    setStatus("请先选择会话", true);
    return;
  }
  const targetAgentID = normalizeAgentID(transferAgentId.value);
  if (!isValidAgentID(targetAgentID)) {
    setStatus("请输入 4 位目标坐席 ID（不能为 0000）", true);
    return;
  }

  actionPending.transfer = true;
  try {
    await apiRequest(`/api/chat/v1/conversations/${activeConversationId.value}/transfer`, {
      method: "POST",
      auth: true,
      body: {
        to_agent_id: Number.parseInt(targetAgentID, 10),
      },
    });
    await refreshConversations();
    await refreshMessages({ conversationID: activeConversationId.value, force: true, forceScrollBottom: true });
    setStatus(`已发起转接到坐席 ${formatAgentID(targetAgentID)}，等待对方确认`);
  } catch (error) {
    handleError(error, "转接失败");
  } finally {
    actionPending.transfer = false;
  }
}

async function confirmTransferConversation(): Promise<void> {
  if (actionPending.confirmTransfer) {
    return;
  }
  if (!activeConversationId.value) {
    setStatus("请先选择会话", true);
    return;
  }
  if (!showConfirmTransfer.value) {
    setStatus("当前会话没有待你确认的转接。", true);
    return;
  }

  actionPending.confirmTransfer = true;
  try {
    await apiRequest(`/api/chat/v1/conversations/${activeConversationId.value}/transfer/confirm`, {
      method: "POST",
      auth: true,
    });
    await refreshConversations();
    await refreshMessages({ conversationID: activeConversationId.value, force: true, forceScrollBottom: true });
    setStatus("已确认转接，当前会话已归你接待");
  } catch (error) {
    handleError(error, "确认转接失败");
  } finally {
    actionPending.confirmTransfer = false;
  }
}

async function rejectTransferConversation(): Promise<void> {
  if (actionPending.rejectTransfer) {
    return;
  }
  if (!activeConversationId.value) {
    setStatus("请先选择会话", true);
    return;
  }
  if (!showConfirmTransfer.value) {
    setStatus("当前会话没有待你处理的转接。", true);
    return;
  }

  actionPending.rejectTransfer = true;
  try {
    await apiRequest(`/api/chat/v1/conversations/${activeConversationId.value}/transfer/reject`, {
      method: "POST",
      auth: true,
    });
    await refreshConversations();
    await refreshMessages({ conversationID: activeConversationId.value, force: true, forceScrollBottom: true });
    setStatus("已拒绝转接，当前会话维持原坐席接待");
  } catch (error) {
    handleError(error, "拒绝转接失败");
  } finally {
    actionPending.rejectTransfer = false;
  }
}

async function closeConversation(): Promise<void> {
  if (actionPending.close) {
    return;
  }
  if (!activeConversationId.value) {
    setStatus("请先选择会话", true);
    return;
  }
  if (!activeCapability.value.isOpen) {
    setStatus("会话已关闭，无法继续操作。", true);
    return;
  }
  if (!activeCapability.value.isMine) {
    setStatus("请先认领会话后再关闭。", true);
    return;
  }
  if (!window.confirm("确认关闭当前会话吗？关闭后将不可继续接待。")) {
    return;
  }

  actionPending.close = true;
  try {
    await apiRequest(`/api/chat/v1/conversations/${activeConversationId.value}/close`, {
      method: "POST",
      auth: true,
    });
    await refreshConversations();
    setStatus("会话已关闭");
  } catch (error) {
    handleError(error, "关闭失败");
  } finally {
    actionPending.close = false;
  }
}

function addQuickReply(): void {
  const text = quickReplyInput.value.trim();
  if (!text) {
    return;
  }
  if (quickReplies.value.includes(text)) {
    quickReplyInput.value = "";
    return;
  }
  quickReplies.value = [text, ...quickReplies.value].slice(0, 30);
  saveQuickReplies();
  quickReplyInput.value = "";
  setStatus("快捷语已新增");
}

function resetQuickReplies(): void {
  quickReplies.value = [...DEFAULT_QUICK_REPLIES];
  saveQuickReplies();
  setStatus("快捷语已重置");
}

async function insertQuickReply(text: string): Promise<void> {
  if (!text) {
    return;
  }
  draftMessage.value = draftMessage.value.trim() ? `${draftMessage.value.trim()}\n${text}` : text;
  await focusComposer();
}

function quickRepliesStorageKey(): string {
  return me.value?.agent_id ? `${STORAGE_KEYS.quickRepliesPrefix}${me.value.agent_id}` : "";
}

function readCursorStorageKey(): string {
  return me.value?.agent_id ? `${STORAGE_KEYS.readCursorPrefix}${me.value.agent_id}` : "";
}

function loadQuickReplies(): string[] {
  const key = quickRepliesStorageKey();
  if (!key) {
    return [...DEFAULT_QUICK_REPLIES];
  }
  const raw = window.localStorage.getItem(key);
  if (!raw) {
    return [...DEFAULT_QUICK_REPLIES];
  }
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) {
      return [...DEFAULT_QUICK_REPLIES];
    }
    const normalized = parsed
      .map((item) => String(item || "").trim())
      .filter((item) => Boolean(item))
      .slice(0, 30);
    return normalized.length > 0 ? normalized : [...DEFAULT_QUICK_REPLIES];
  } catch {
    return [...DEFAULT_QUICK_REPLIES];
  }
}

function saveQuickReplies(): void {
  const key = quickRepliesStorageKey();
  if (!key) {
    return;
  }
  window.localStorage.setItem(key, JSON.stringify(quickReplies.value));
}

function loadReadCursor(): Record<string, number> {
  const key = readCursorStorageKey();
  if (!key) {
    return {};
  }
  const raw = window.localStorage.getItem(key);
  if (!raw) {
    return {};
  }
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== "object") {
      return {};
    }
    return parsed as Record<string, number>;
  } catch {
    return {};
  }
}

function saveReadCursor(): void {
  const key = readCursorStorageKey();
  if (!key) {
    return;
  }
  window.localStorage.setItem(key, JSON.stringify(readCursor.value));
}

function sortConversations(items: Conversation[]): Conversation[] {
  return [...items].sort((a, b) => Date.parse(b.updated_at || b.created_at) - Date.parse(a.updated_at || a.created_at));
}

function extractErrorMessage(value: unknown, fallback = ""): string {
  if (value == null) {
    return fallback;
  }
  if (typeof value === "string") {
    const text = value.trim();
    return text || fallback;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    return (
      extractErrorMessage(record.message, "") ||
      extractErrorMessage(record.error, "") ||
      extractErrorMessage(record.detail, "") ||
      extractErrorMessage(record.reason, "") ||
      fallback
    );
  }
  return fallback;
}

function isAbortError(error: unknown): boolean {
  return Boolean(error && typeof error === "object" && "name" in error && (error as { name?: string }).name === "AbortError");
}

function safeUUID(): string {
  if (window.crypto?.randomUUID) {
    return window.crypto.randomUUID();
  }
  return `${Date.now()}_${Math.random().toString(36).slice(2, 10)}`;
}

function buildAIAssistInsight(
  conversation: Conversation,
  currentMessages: NormalizedMessage[],
  currentQuickReplies: string[],
  meID: number,
): AIInsight {
  const visitorMessages = currentMessages.filter((item) => item.sender_type === "visitor");
  const aiMessages = currentMessages.filter((item) => item.sender_type === "ai");
  const agentMessages = currentMessages.filter((item) => item.sender_type === "agent");
  const lastVisitorMessage = visitorMessages[visitorMessages.length - 1] || null;
  const lastAIMessage = aiMessages[aiMessages.length - 1] || null;
  const lastAgentMessage = agentMessages[agentMessages.length - 1] || null;
  const intent = detectConversationIntent(visitorMessages, lastVisitorMessage);
  const action = deriveAIAssistAction(conversation, intent, lastVisitorMessage, aiMessages, lastAgentMessage, meID);

  return {
    badgeText: action.badgeText,
    badgeTone: action.badgeTone,
    intentTitle: intent.label,
    intentDetail: intent.detail,
    actionTitle: action.title,
    actionDetail: action.detail,
    summary: buildAIConversationSummary(conversation, {
      intent,
      lastVisitorMessage,
      aiMessages,
      agentMessages,
    }),
    lastAIReplyText: lastAIMessage ? `${formatMessageTime(lastAIMessage.created_at)} · ${lastAIMessage.content || ""}` : "暂无 AI 回复",
    suggestions: buildAISuggestedReplies(conversation, {
      intent,
      lastVisitorMessage,
      aiMessages,
      lastAgentMessage,
      quickReplies: currentQuickReplies,
      meID,
    }),
  };
}

function detectConversationIntent(visitorMessages: NormalizedMessage[], lastVisitorMessage: NormalizedMessage | null): AIIntent {
  const recentVisitorText = [String(lastVisitorMessage?.content || ""), ...visitorMessages.slice(-3).map((item) => String(item.content || ""))]
    .join("\n")
    .trim();

  for (const rule of AI_INTENT_RULES) {
    if (rule.pattern.test(recentVisitorText)) {
      return {
        key: rule.key,
        label: rule.label,
        detail: rule.detail,
      };
    }
  }

  if (String(lastVisitorMessage?.content || "").trim()) {
    return {
      key: "general",
      label: "通用咨询",
      detail: "先确认核心问题和必要信息，再给出下一步处理路径。",
    };
  }

  return {
    key: "idle",
    label: "等待更多信息",
    detail: "当前消息较少，适合先做欢迎和信息收集。",
  };
}

function deriveAIAssistAction(
  conversation: Conversation,
  intent: AIIntent,
  lastVisitorMessage: NormalizedMessage | null,
  aiMessages: NormalizedMessage[],
  lastAgentMessage: NormalizedMessage | null,
  meID: number,
): AIAction {
  const assignedAgentID = Number(conversation.assigned_agent_id || 0);
  const pendingTransferTo = Number(conversation.pending_transfer_to_agent_id || 0);

  if (conversation.status === "closed") {
    return {
      badgeText: "已关闭",
      badgeTone: "default",
      title: "复盘归档",
      detail: "会话已结束，可结合摘要补充备注或复盘常见问题。",
    };
  }

  if (pendingTransferTo > 0 && pendingTransferTo === meID && assignedAgentID !== meID) {
    return {
      badgeText: "待确认转接",
      badgeTone: "warn",
      title: "优先确认转接",
      detail: "当前会话已转给你待确认，先确认后再继续发送消息。",
    };
  }

  if (assignedAgentID <= 0) {
    return {
      badgeText: aiMessages.length > 0 ? "AI 已兜底" : "待认领",
      badgeTone: aiMessages.length > 0 ? "success" : "warn",
      title: "尽快认领会话",
      detail: aiMessages.length > 0 ? "AI 已先行回复，建议你尽快接手，避免访客重复描述。" : "当前无人接待，建议优先认领并在 60 秒内完成人工首响。",
    };
  }

  if (assignedAgentID === meID) {
    return {
      badgeText: aiMessages.length > 0 ? "AI 协作中" : "人工接待中",
      badgeTone: aiMessages.length > 0 ? "success" : "brand",
      title: intent.key === "complaint" ? "先安抚再核查" : "继续跟进",
      detail:
        intent.key === "complaint"
          ? "先明确你已接手并表达歉意，再收集订单号与问题时间点。"
          : lastVisitorMessage
            ? "根据访客最近一条消息补齐关键信息，再给出明确下一步。"
            : lastAgentMessage
              ? "当前可继续推进核实、转接或关闭动作。"
              : "适合先发送确认型回复，告诉访客你正在处理。",
    };
  }

  return {
    badgeText: "协作会话",
    badgeTone: "default",
    title: "等待当前坐席处理",
    detail: "该会话已被其他坐席接待，可重点关注需要你确认的转接提醒。",
  };
}

function buildAIConversationSummary(
  conversation: Conversation,
  context: {
    intent: AIIntent;
    lastVisitorMessage: NormalizedMessage | null;
    aiMessages: NormalizedMessage[];
    agentMessages: NormalizedMessage[];
  },
): string {
  const fragments: string[] = [];
  fragments.push(`会话当前${conversation.status === "closed" ? "已关闭" : "进行中"}，站点 ${conversation.site_id || "-"}。`);
  if (context.lastVisitorMessage?.content) {
    fragments.push(`访客最近在问“${truncateText(context.lastVisitorMessage.content, 42)}”。`);
  } else {
    fragments.push("当前访客侧信息较少，适合先做欢迎和信息收集。");
  }
  if (context.intent.key !== "idle") {
    fragments.push(`主要意图偏向${context.intent.label}。`);
  }
  if (context.aiMessages.length > 0) {
    fragments.push(`AI 已回复 ${context.aiMessages.length} 次。`);
  } else if (Number(conversation.assigned_agent_id || 0) <= 0) {
    fragments.push("当前仍未分配人工坐席。");
  }
  if (context.agentMessages.length > 0) {
    fragments.push(`人工侧累计回复 ${context.agentMessages.length} 次。`);
  }
  return fragments.join("");
}

function buildAISuggestedReplies(
  conversation: Conversation,
  context: {
    intent: AIIntent;
    lastVisitorMessage: NormalizedMessage | null;
    aiMessages: NormalizedMessage[];
    lastAgentMessage: NormalizedMessage | null;
    quickReplies: string[];
    meID: number;
  },
): string[] {
  const suggestions: string[] = [];
  const intentKey = context.intent.key || "general";
  const assignedAgentID = Number(conversation.assigned_agent_id || 0);

  if (conversation.status === "closed") {
    return [];
  }
  if (assignedAgentID > 0 && assignedAgentID !== context.meID) {
    return [];
  }

  if (assignedAgentID <= 0) {
    suggestions.push("您好，我先接手处理这个问题，请稍等，我先帮您核实。");
  } else if (context.aiMessages.length > 0) {
    suggestions.push("我已接手，下面由我继续为您跟进处理。");
  }

  if (intentKey === "complaint") {
    suggestions.push("抱歉让您久等了，我先优先处理。麻烦把订单号和问题经过发我，我马上核实。");
  } else if (intentKey === "refund") {
    suggestions.push("收到，我先帮您确认退款/售后进度，请提供订单号和申请时间。");
  } else if (intentKey === "logistics") {
    suggestions.push("我先帮您查订单物流，请发我订单号或下单手机号。");
  } else if (intentKey === "recommendation") {
    suggestions.push("可以，我先根据您的预算、使用场景和偏好帮您推荐。");
  } else if (intentKey === "price") {
    suggestions.push("我先帮您核对这款商品当前的活动和优惠门槛，请稍等。");
  } else if (intentKey === "account") {
    suggestions.push("我先帮您排查账号问题，请把报错提示和操作步骤发我。");
  } else if (intentKey === "payment") {
    suggestions.push("我先帮您确认支付/开票状态，请提供订单号和支付时间。");
  } else if (intentKey === "manual") {
    suggestions.push("您好，我已经人工接入，接下来由我继续为您处理。");
  }

  if (context.lastVisitorMessage?.content) {
    suggestions.push("收到您的信息，我正在核实中，稍后给您明确结果。");
  } else if (context.lastAgentMessage) {
    suggestions.push("我继续为您跟进处理，如有进展会第一时间同步您。");
  }

  for (const reply of context.quickReplies.slice(0, 3)) {
    suggestions.push(reply);
  }

  return dedupeReplySuggestions(suggestions).slice(0, 5);
}

function dedupeReplySuggestions(items: string[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const item of items) {
    const text = String(item || "").trim();
    if (!text) {
      continue;
    }
    const key = text.replace(/\s+/g, "");
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    result.push(text);
  }
  return result;
}
</script>

<template>
  <div class="console-root">
    <div class="console-shell">
      <ConsoleHeader
        title="InlineChat 客服工作台"
        subtitle="保留旧版客服台的实时消息、转接、未读、快捷语和 AI 协作逻辑。"
        :user-text="userText"
        role-text="客服角色"
        :theme-text="themeText"
        role-tone="success"
        @toggle-theme="toggleTheme"
        @logout="logout"
      />

      <section class="metric-grid">
        <MetricCard label="总会话" :value="queueTotalCount" hint="全量会话快照" />
        <MetricCard label="进行中" :value="queueOpenCount" hint="open 状态" />
        <MetricCard label="待接入" :value="queueWaitingCount" hint="未分配 open 会话" />
        <MetricCard label="我的接待" :value="queueMineCount" hint="当前归属给我" />
        <MetricCard label="已关闭" :value="queueClosedCount" hint="closed 状态" />
        <MetricCard label="未分配" :value="queueUnassignedCount" hint="无 assigned_agent_id" />
        <MetricCard label="访客未读" :value="unreadTotalCount" hint="本地读游标计算" />
      </section>

      <section class="header-meta">
        <StatusPill id="agentBrief" :text="agentBriefText" tone="brand" />
        <StatusPill id="wsStateBadge" :text="wsStateText" :tone="wsTone" />
      </section>

      <section class="workspace agent">
        <PanelCard title="会话队列" description="保留旧版筛选、未读与待确认转接逻辑。">
          <template #actions>
            <div class="panel-actions">
              <button id="refreshConversationsBtn" class="toolbar-button" type="button" @click="refreshAll">
                {{ loading ? "刷新中..." : "刷新" }}
              </button>
            </div>
          </template>

          <div class="queue-tabs" id="queueTabs">
            <button
              id="queueTabAll"
              class="queue-tab"
              :class="{ active: queueMode === 'all' }"
              type="button"
              @click="setQueueMode('all')"
            >
              全部 {{ queueTotalCount }}
            </button>
            <button
              id="queueTabOpen"
              class="queue-tab"
              :class="{ active: queueMode === 'open' }"
              type="button"
              @click="setQueueMode('open')"
            >
              进行中 {{ queueOpenCount }}
            </button>
            <button
              id="queueTabClosed"
              class="queue-tab"
              :class="{ active: queueMode === 'closed' }"
              type="button"
              @click="setQueueMode('closed')"
            >
              已关闭 {{ queueClosedCount }}
            </button>
          </div>

          <div class="form-grid two-column">
            <label class="label-stack">
              <span>搜索会话</span>
              <input
                id="conversationSearchInput"
                v-model.trim="conversationSearch"
                class="search-field"
                placeholder="按会话 ID / Site ID 搜索"
              />
            </label>
            <label class="label-stack">
              <span>站点锁定</span>
              <input id="siteFilterInput" class="text-field" :value="siteFilter || '--'" readonly />
            </label>
          </div>

          <div class="toggle-row">
            <label class="checkbox-field">
              <input id="unassignedOnlyCheckbox" v-model="unassignedOnly" type="checkbox" @change="handleUnassignedOnlyChange" />
              仅未分配
            </label>
            <label class="checkbox-field">
              <input id="mineOnlyCheckbox" v-model="mineOnly" type="checkbox" @change="handleMineOnlyChange" />
              仅我的会话
            </label>
          </div>

          <section v-if="pendingTransferItems.length > 0" id="transferReminderBox" class="reminder-box">
            <div class="reminder-head">
              <h3>待确认转接</h3>
              <span id="transferReminderCount" class="reminder-count">{{ pendingTransferItems.length }}</span>
            </div>
            <div id="transferReminderList" class="reminder-list">
              <button
                v-for="item in pendingTransferItems"
                :key="item.id"
                class="reminder-item"
                :class="{ active: String(item.id) === activeConversationId }"
                type="button"
                @click="jumpToTransferReminderConversation(String(item.id))"
              >
                <strong>会话 #{{ item.id }}</strong>
                <span>
                  来源坐席 {{ item.assigned_agent_id ? formatAgentID(item.assigned_agent_id) : "-" }} ·
                  {{ formatDurationSince(item.updated_at || item.created_at) }}前
                </span>
              </button>
            </div>
          </section>

          <div id="conversationList" class="stack scroll-stack conversation-stack">
            <article
              v-for="item in filteredConversations"
              :key="item.id"
              class="conversation-item list-card"
              :class="{ active: String(item.id) === activeConversationId }"
              @click="selectConversation(item)"
            >
              <div class="panel-head conversation-head">
                <div class="conversation-title-row">
                  <strong>#{{ item.id }}</strong>
                  <StatusPill :text="item.status === 'closed' ? '已关闭' : '进行中'" :tone="item.status === 'closed' ? 'default' : 'success'" />
                  <span v-if="unreadMap[String(item.id)]" class="unread-badge">
                    {{ unreadMap[String(item.id)] > 99 ? "99+" : unreadMap[String(item.id)] }}
                  </span>
                </div>
              </div>
              <div class="meta-row">
                <span>{{ item.assigned_agent_id ? `坐席 ${formatAgentID(item.assigned_agent_id)}` : "未分配" }}</span>
                <span v-if="item.pending_transfer_to_agent_id">待确认转接→{{ formatAgentID(item.pending_transfer_to_agent_id) }}</span>
                <span>site={{ item.site_id }}</span>
              </div>
              <div class="meta-row">
                <span>更新时间 {{ formatTime(item.updated_at) }}</span>
                <span>{{ formatDurationSince(item.updated_at || item.created_at) }}前</span>
              </div>
            </article>

            <div v-if="filteredConversations.length === 0" class="empty-state">暂无会话</div>
          </div>
        </PanelCard>

        <PanelCard
          title="会话工作区"
          :description="activeConversation ? `状态 ${activeConversation.status} · site=${activeConversation.site_id}` : '请先在左侧选择会话'"
        >
          <template #actions>
            <div class="panel-actions">
              <button
                id="confirmTransferBtn"
                class="toolbar-button"
                type="button"
                :disabled="!canConfirmTransfer"
                v-show="showConfirmTransfer"
                @click="confirmTransferConversation"
              >
                确认转接
              </button>
              <button
                id="rejectTransferBtn"
                class="ghost-button"
                type="button"
                :disabled="!canRejectTransfer"
                v-show="showConfirmTransfer"
                @click="rejectTransferConversation"
              >
                拒绝转接
              </button>
              <button id="claimBtn" class="ghost-button" type="button" :disabled="!canClaim" @click="claimConversation">认领</button>
              <div class="transfer-box">
                <input
                  id="transferAgentIdInput"
                  v-model="transferAgentId"
                  class="text-field compact-input"
                  inputmode="numeric"
                  maxlength="4"
                  placeholder="目标坐席ID(4位)"
                  :disabled="!canTransfer"
                />
                <button id="transferBtn" class="ghost-button" type="button" :disabled="!canTransfer" @click="transferConversation">
                  转接
                </button>
              </div>
              <button id="closeBtn" class="danger-button" type="button" :disabled="!canClose" @click="closeConversation">关闭</button>
            </div>
          </template>

          <div class="detail-strip">
            <div>
              <h3 id="activeConversationTitle">{{ activeConversation ? `会话 #${activeConversation.id}` : "未选择会话" }}</h3>
              <div id="activeConversationMeta" class="meta-row">
                <span>站点 {{ activeConversation?.site_id ?? "-" }}</span>
                <span>分配 {{ activeConversation?.assigned_agent_id ? formatAgentID(activeConversation.assigned_agent_id) : "未分配" }}</span>
                <span v-if="activeConversation?.pending_transfer_to_agent_id">
                  待确认转接→{{ formatAgentID(activeConversation.pending_transfer_to_agent_id) }}
                </span>
                <span>
                  状态 <strong id="detailStatus" class="status-strong">{{ activeConversation?.status ?? "-" }}</strong>
                </span>
              </div>
            </div>
          </div>

          <div id="agentMessages" ref="messageListRef" class="message-list">
            <article
              v-for="message in messages"
              :key="`${message.id}-${message.client_msg_id}`"
              class="message-bubble"
              :class="{
                self: isMineMessage(message),
                ai: message.sender_type === 'ai',
                system: message.sender_type === 'system',
              }"
            >
              <div v-if="message.sender_type !== 'system'" class="message-head">
                <span class="message-sender">{{ formatMessageSenderName(message) }}</span>
              </div>
              <div class="bubble">
                <div class="bubble-content">{{ message.content }}</div>
              </div>
              <button
                v-if="canRetryMessage(message)"
                class="message-meta retryable-button"
                type="button"
                @click="resendMessage(message.client_msg_id)"
              >
                {{ formatMessageFooterText(message) }}
              </button>
              <span v-else-if="message.sender_type !== 'system'" class="message-meta">{{ formatMessageFooterText(message) }}</span>
            </article>
            <div v-if="messages.length === 0" class="empty-state">暂无消息</div>
          </div>

          <div class="form-grid send-form">
            <textarea
              id="agentContentInput"
              ref="composerRef"
              v-model="draftMessage"
              class="text-area"
              :disabled="!activeCapability.hasConversation || !activeCapability.isOpen || !activeCapability.isMine || actionPending.send"
              placeholder="输入消息，回车发送（Shift+回车换行）"
              @keydown="handleComposerKeydown"
            />
            <div class="panel-actions">
              <button id="agentSendBtn" class="primary-button" type="button" :disabled="!canSend" @click="sendMessage">
                {{ sending ? "发送中..." : "发送消息" }}
              </button>
            </div>
          </div>
        </PanelCard>

        <PanelCard title="协作侧栏" description="旧版会话详情、快捷语和 AI 协作逻辑已经迁回。">
          <div class="stack scroll-stack sidebar-stack">
            <section class="list-card">
              <div class="panel-head">
                <h4>会话详情</h4>
                <StatusPill id="detailWsState" :text="wsConnected ? '已连接' : wsReconnectPending ? '重连中' : '未连接'" :tone="wsTone" />
              </div>
              <dl class="info-list">
                <div><dt>会话ID</dt><dd id="detailConversationId">{{ activeConversation?.id ?? "-" }}</dd></div>
                <div><dt>状态</dt><dd>{{ activeConversation?.status ?? "-" }}</dd></div>
                <div><dt>Site ID</dt><dd id="detailSiteId">{{ activeConversation?.site_id ?? "-" }}</dd></div>
                <div><dt>分配坐席</dt><dd id="detailAssigned">{{ activeConversation?.assigned_agent_id ? formatAgentID(activeConversation.assigned_agent_id) : "未分配" }}</dd></div>
                <div><dt>最近活跃</dt><dd id="detailUpdatedAt">{{ formatTime(activeConversation?.updated_at) }}</dd></div>
                <div><dt>等待时长</dt><dd id="detailWaitingDuration">{{ formatDurationSince(activeConversation?.updated_at || activeConversation?.created_at) }}</dd></div>
              </dl>
            </section>

            <section class="list-card">
              <div class="panel-head">
                <h4>快捷语</h4>
                <button id="resetQuickRepliesBtn" class="ghost-button" type="button" @click="resetQuickReplies">重置默认</button>
              </div>
              <div id="quickReplyList" class="chip-list">
                <button
                  v-for="reply in quickReplies"
                  :key="reply"
                  class="ghost-button chip-button"
                  type="button"
                  @click="insertQuickReply(reply)"
                >
                  {{ reply }}
                </button>
              </div>
              <form id="quickReplyForm" class="inline-form" @submit.prevent="addQuickReply">
                <input
                  id="quickReplyInput"
                  v-model.trim="quickReplyInput"
                  class="text-field"
                  placeholder="新增快捷语，点击后可一键填充输入框"
                />
                <button class="ghost-button" type="submit">新增</button>
              </form>
            </section>

            <section class="list-card">
              <div class="panel-head">
                <h4>AI 协作</h4>
                <StatusPill id="aiAssistBadge" :text="aiInsight.badgeText" :tone="aiInsight.badgeTone" />
              </div>

              <div class="ai-insight-grid">
                <article class="ai-signal-card">
                  <span>访客意图</span>
                  <strong id="aiIntentTitle">{{ aiInsight.intentTitle }}</strong>
                  <p id="aiIntentDetail">{{ aiInsight.intentDetail }}</p>
                </article>
                <article class="ai-signal-card">
                  <span>建议动作</span>
                  <strong id="aiActionTitle">{{ aiInsight.actionTitle }}</strong>
                  <p id="aiActionDetail">{{ aiInsight.actionDetail }}</p>
                </article>
              </div>

              <div class="ai-summary-block">
                <div class="ai-section-title">对话摘要</div>
                <p id="aiConversationSummary">{{ aiInsight.summary }}</p>
              </div>

              <div class="ai-summary-block">
                <div class="ai-section-title">建议回复</div>
                <div id="aiSuggestionList" class="chip-list">
                  <button
                    v-for="suggestion in aiInsight.suggestions"
                    :key="suggestion"
                    class="ghost-button chip-button"
                    type="button"
                    @click="insertQuickReply(suggestion)"
                  >
                    {{ suggestion }}
                  </button>
                  <div v-if="aiInsight.suggestions.length === 0" class="empty-state compact-empty">暂无建议回复</div>
                </div>
              </div>

              <div class="ai-summary-block">
                <div class="ai-section-title">最近 AI 回复</div>
                <p id="aiLastReplyText">{{ aiInsight.lastAIReplyText }}</p>
              </div>
            </section>
          </div>
        </PanelCard>
      </section>

      <footer id="statusLine" class="status-line" :class="{ error: statusError }">{{ statusText }}</footer>
    </div>
  </div>
</template>
