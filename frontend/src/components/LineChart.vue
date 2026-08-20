<template>
  <Line :data="data" :options="options" />
</template>

<script setup lang="ts">
import { Line } from 'vue-chartjs'
import {
  Chart as ChartJS, Title, Tooltip, Legend,
  PointElement, LineElement, CategoryScale, LinearScale, Filler
} from 'chart.js'

ChartJS.register(Title, Tooltip, Legend, PointElement, LineElement,
                 CategoryScale, LinearScale, Filler)

defineProps<{
  data: any
  options?: any
}>()
</script>
