<template>
  <v-card>
    <v-card-title class="d-flex align-center flex-wrap" style="gap: 8px">
      ガントチャート (製造指示)
      <v-spacer />
      <v-btn-toggle v-model="zoom" mandatory density="compact" variant="outlined">
        <v-btn value="day">日</v-btn>
        <v-btn value="week">週</v-btn>
      </v-btn-toggle>
      <v-btn variant="text" prepend-icon="mdi-refresh" @click="load">更新</v-btn>
    </v-card-title>
    <v-card-text>
      <p class="text-body-2 text-medium-emphasis">
        各バーの長さは <code>startDate ～ dueDate</code> の期間。色はステータスを表します。
        スクロール可能 — 全 {{ rows.length }} 件を表示中。
      </p>

      <div v-if="!rows.length" class="text-medium-emphasis pa-4 text-center">
        製造指示がありません
      </div>

      <div v-else class="gantt-wrap">
        <!-- Header row: dates -->
        <div class="gantt-row gantt-header">
          <div class="gantt-label">WO / 品目</div>
          <div class="gantt-track">
            <div
              v-for="(d, idx) in dateAxis" :key="idx"
              class="gantt-tick"
              :style="{ width: cellWidth + 'px' }"
              :class="{ weekend: isWeekend(d) }"
            >
              {{ tickLabel(d, idx) }}
            </div>
          </div>
        </div>

        <!-- Body rows -->
        <div v-for="w in rows" :key="w.id" class="gantt-row">
          <div class="gantt-label">
            <div>{{ w.orderNo }}</div>
            <div class="text-caption text-medium-emphasis">
              {{ codeMap[w.itemId] }} ({{ w.quantity }})
            </div>
          </div>
          <div class="gantt-track" :style="{ width: trackWidth + 'px' }">
            <!-- Today line -->
            <div
              v-if="todayOffset !== null"
              class="gantt-today"
              :style="{ left: todayOffset + 'px' }"
            />
            <div
              class="gantt-bar"
              :style="barStyle(w)"
              :class="'status-' + w.status"
            >
              {{ w.status }}
            </div>
          </div>
        </div>

        <!-- Legend -->
        <div class="gantt-legend">
          <span v-for="s in statuses" :key="s" class="gantt-legend-item">
            <span class="gantt-swatch" :class="'status-' + s" /> {{ s }}
          </span>
          <span class="gantt-legend-item">
            <span class="gantt-today-swatch" /> 本日
          </span>
        </div>
      </div>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ItemsApi, WorkOrdersApi, type Item, type WorkOrder } from '@/api'

const items = ref<Item[]>([])
const rows = ref<WorkOrder[]>([])
const zoom = ref<'day' | 'week'>('day')

const codeMap = computed(() => {
  const m: Record<string, string> = {}
  for (const i of (items.value ?? [])) if (i.id) m[i.id] = `${i.code}`
  return m
})

const statuses = ['PLANNED', 'RELEASED', 'IN_PROGRESS', 'COMPLETED', 'CLOSED']

async function load() {
  const [_a, _b] = await Promise.all([ItemsApi.list(), WorkOrdersApi.list()])
  items.value = _a ?? []
  rows.value = _b ?? []
}
onMounted(load)

// Compute axis range from earliest start to latest due
const range = computed(() => {
  if (!(rows.value ?? []).length) {
    const t = new Date()
    return { start: t, end: new Date(t.getTime() + 28 * 86400000) }
  }
  let min = new Date(rows.value[0].startDate)
  let max = new Date(rows.value[0].dueDate)
  for (const w of (rows.value ?? [])) {
    const s = new Date(w.startDate)
    const d = new Date(w.dueDate)
    if (s < min) min = s
    if (d > max) max = d
  }
  // pad
  min = new Date(min.getTime() - 2 * 86400000)
  max = new Date(max.getTime() + 2 * 86400000)
  return { start: min, end: max }
})

const totalDays = computed(() => {
  const ms = range.value.end.getTime() - range.value.start.getTime()
  return Math.max(1, Math.ceil(ms / 86400000))
})
const cellWidth = computed(() => zoom.value === 'day' ? 28 : 14)
const trackWidth = computed(() => totalDays.value * cellWidth.value)

const dateAxis = computed(() => {
  const arr: Date[] = []
  const start = range.value.start
  for (let i = 0; i < totalDays.value; i++) {
    arr.push(new Date(start.getTime() + i * 86400000))
  }
  return arr
})

function tickLabel(d: Date, idx: number) {
  if (zoom.value === 'day') {
    return d.getDate() === 1 ? `${d.getMonth() + 1}/${d.getDate()}` : `${d.getDate()}`
  }
  return idx % 7 === 0 ? `${d.getMonth() + 1}/${d.getDate()}` : ''
}
function isWeekend(d: Date) { return d.getDay() === 0 || d.getDay() === 6 }

function barStyle(w: WorkOrder) {
  const s = new Date(w.startDate)
  const e = new Date(w.dueDate)
  const left = (s.getTime() - range.value.start.getTime()) / 86400000 * cellWidth.value
  const width = Math.max(cellWidth.value * 0.5,
                         (e.getTime() - s.getTime()) / 86400000 * cellWidth.value)
  return { left: left + 'px', width: width + 'px' }
}

const todayOffset = computed(() => {
  const t = new Date()
  if (t < range.value.start || t > range.value.end) return null
  return (t.getTime() - range.value.start.getTime()) / 86400000 * cellWidth.value
})
</script>

<style scoped>
.gantt-wrap {
  overflow-x: auto;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 6px;
  background: rgb(var(--v-theme-surface));
}
.gantt-row {
  display: flex;
  border-bottom: 1px solid rgba(var(--v-border-color), 0.4);
  min-height: 44px;
  align-items: stretch;
}
.gantt-row:last-child { border-bottom: none; }

.gantt-header {
  position: sticky; top: 0; z-index: 2;
  background: rgb(var(--v-theme-surface-light, var(--v-theme-surface)));
  font-weight: 500;
  border-bottom: 2px solid rgba(var(--v-border-color), var(--v-border-opacity));
}

.gantt-label {
  flex: 0 0 200px;
  padding: 6px 10px;
  border-right: 1px solid rgba(var(--v-border-color), 0.4);
  display: flex; flex-direction: column; justify-content: center;
  font-size: 0.85em;
  background: rgb(var(--v-theme-surface));
  position: sticky; left: 0; z-index: 1;
}

.gantt-track {
  position: relative;
  display: flex;
  flex: 1;
  min-height: 44px;
}

.gantt-tick {
  flex-shrink: 0;
  text-align: center;
  font-size: 0.7em;
  border-right: 1px solid rgba(var(--v-border-color), 0.2);
  padding: 4px 0;
  color: rgba(var(--v-theme-on-surface), 0.6);
}
.gantt-tick.weekend {
  background: rgba(var(--v-theme-on-surface), 0.04);
}

.gantt-bar {
  position: absolute;
  top: 8px;
  height: 26px;
  border-radius: 4px;
  color: white;
  font-size: 0.7em;
  display: flex;
  align-items: center;
  padding: 0 6px;
  white-space: nowrap;
  overflow: hidden;
  box-shadow: 0 1px 2px rgba(0,0,0,0.2);
}

.gantt-bar.status-PLANNED     { background: #757575; }
.gantt-bar.status-RELEASED    { background: #2E7D32; }
.gantt-bar.status-IN_PROGRESS { background: #EF6C00; }
.gantt-bar.status-COMPLETED   { background: #1976D2; }
.gantt-bar.status-CLOSED      { background: #1565C0; }

.gantt-today {
  position: absolute;
  top: 0; bottom: 0;
  width: 2px;
  background: #C62828;
  z-index: 1;
}

.gantt-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  padding: 10px 14px;
  border-top: 1px solid rgba(var(--v-border-color), 0.4);
  font-size: 0.85em;
  background: rgba(var(--v-theme-on-surface), 0.02);
}
.gantt-legend-item { display: inline-flex; align-items: center; gap: 6px; }
.gantt-swatch {
  display: inline-block; width: 16px; height: 14px; border-radius: 3px;
}
.gantt-swatch.status-PLANNED     { background: #757575; }
.gantt-swatch.status-RELEASED    { background: #2E7D32; }
.gantt-swatch.status-IN_PROGRESS { background: #EF6C00; }
.gantt-swatch.status-COMPLETED   { background: #1976D2; }
.gantt-swatch.status-CLOSED      { background: #1565C0; }
.gantt-today-swatch {
  display: inline-block; width: 2px; height: 14px;
  background: #C62828; border: 1px solid #C62828;
}
</style>
