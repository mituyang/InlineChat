<script setup lang="ts">
import StatusPill from "./StatusPill.vue";

defineProps<{
  title: string;
  subtitle: string;
  userText: string;
  roleText: string;
  themeText: string;
  roleTone?: "default" | "brand" | "success" | "warn" | "danger";
}>();

const emit = defineEmits<{
  logout: [];
  toggleTheme: [];
}>();
</script>

<template>
  <header class="glass-card topbar">
    <div>
      <h1>{{ title }}</h1>
      <p>{{ subtitle }}</p>
    </div>
    <div class="topbar-actions">
      <StatusPill :text="roleText" :tone="roleTone ?? 'brand'" />
      <StatusPill id="userBox" :text="userText" />
      <button class="toolbar-button" type="button" @click="emit('toggleTheme')">{{ themeText }}</button>
      <button id="logoutBtn" class="toolbar-button" type="button" @click="emit('logout')">退出登录</button>
    </div>
  </header>
</template>
