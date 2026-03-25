<script setup lang="ts">
defineProps<{
  text: string;
  tone?: "default" | "brand" | "success" | "warn" | "danger";
}>();
</script>

<template>
  <span class="inline-chip" :class="tone === 'default' ? '' : tone">{{ text }}</span>
</template>
