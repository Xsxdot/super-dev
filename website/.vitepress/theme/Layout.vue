<script setup lang="ts">
import { useData } from 'vitepress'
import DefaultTheme from 'vitepress/theme'
import Home from './components/Home.vue'

const { frontmatter } = useData()
</script>

<template>
  <!-- Bespoke marketing home; every other page (future docs) uses the default theme layout -->
  <Home v-if="frontmatter.layout === 'home'" />
  <DefaultTheme.Layout v-else />
</template>
