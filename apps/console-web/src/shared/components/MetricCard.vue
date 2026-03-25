<script setup lang="ts">
defineProps<{
  label: string;
  value: string | number;
  hint?: string;
}>();
</script>

<template>
  <article class="glass-card metric-card">
    <span>{{ label }}</span>
    <strong>{{ value }}</strong>
    <p v-if="hint">{{ hint }}</p>
  </article>
</template>
