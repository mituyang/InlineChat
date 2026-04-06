export type Role = "agent" | "admin" | "super_admin";

export interface MeResponse {
  agent_id: number;
  email: string;
  role: Role;
  site_id?: string;
}

export interface Conversation {
  id: number;
  status: "open" | "closed";
  site_id: string;
  assigned_agent_id?: number;
  pending_transfer_to_agent_id?: number;
  created_at: string;
  updated_at: string;
}

export interface Message {
  id: number;
  conversation_id: number;
  sender_type: "visitor" | "agent" | "ai" | "system";
  sender_id?: string;
  content: string;
  client_msg_id?: string;
  status?: string;
  created_at: string;
  updated_at: string;
}

export interface Site {
  id: number;
  site_id: string;
  name: string;
  domains: string[];
  widget_key: string;
  status: "active" | "disabled";
  created_at: string;
  updated_at: string;
}

export interface AdminAgent {
  id: number;
  email: string;
  display_name: string;
  site_id: string;
  role: Role;
  status: "active" | "inactive";
  created_at: string;
  updated_at: string;
}

export interface SiteAIConfig {
  site_id: string;
  enabled: boolean;
  reply_mode: string;
  updated_at?: string;
  chunk_count?: number;
  reloaded_at?: string;
}
