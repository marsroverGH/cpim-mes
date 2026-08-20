<template>
  <div>
  <v-row>
    <v-col cols="12" md="5">
      <v-card>
        <v-card-title class="d-flex align-center">
          作業カレンダー
          <v-spacer />
          <v-btn color="primary" prepend-icon="mdi-plus" size="small" @click="openNew">新規</v-btn>
        </v-card-title>
        <v-list density="compact">
          <v-list-item
            v-for="c in calendars" :key="c.id"
            :active="selected?.id === c.id"
            @click="select(c)"
          >
            <template #prepend>
              <v-icon icon="mdi-calendar" />
            </template>
            <v-list-item-title>
              {{ c.name }}
              <v-chip v-if="c.isDefault" color="primary" size="x-small" class="ml-1">既定</v-chip>
            </v-list-item-title>
            <v-list-item-subtitle>{{ c.code }}</v-list-item-subtitle>
          </v-list-item>
        </v-list>
      </v-card>
    </v-col>

    <v-col cols="12" md="7">
      <v-card v-if="selected">
        <v-card-title>{{ selected.name }} の設定</v-card-title>
        <v-card-text>
          <v-row dense>
            <v-col cols="12" sm="6">
              <v-text-field v-model="selected.code" label="コード" />
            </v-col>
            <v-col cols="12" sm="6">
              <v-text-field v-model="selected.name" label="名称" />
            </v-col>
            <v-col cols="12">
              <v-checkbox v-model="selected.isDefault" label="既定カレンダー" density="compact" />
            </v-col>
          </v-row>

          <div class="text-subtitle-2 mt-2">標準週次パターン (分)</div>
          <v-row dense>
            <v-col v-for="(d, i) in days" :key="i" cols="6" sm="3" md="3">
              <v-text-field
                v-model.number="(selected as any)[d.field]"
                :label="d.label" type="number" hide-details density="compact"
              />
            </v-col>
          </v-row>

          <v-card-actions class="px-0">
            <v-btn color="primary" :loading="busy" @click="saveCal">保存</v-btn>
            <v-btn variant="text" color="error" @click="removeCal">削除</v-btn>
          </v-card-actions>

          <v-divider class="my-3" />

          <div class="text-subtitle-2 d-flex align-center">
            例外 (祝日 / 振替出勤)
            <v-spacer />
            <v-btn size="small" prepend-icon="mdi-plus" @click="openException">追加</v-btn>
          </div>
          <v-data-table
            :items="exceptions" :headers="exHeaders" density="compact" :items-per-page="-1"
            class="mt-2"
          >
            <template #item.exceptionDate="{ item }">{{ fmt(item.exceptionDate) }}</template>
            <template #item.kind="{ item }">
              <v-chip size="x-small" :color="item.kind === 'HOLIDAY' ? 'error' : 'success'">
                {{ item.kind }}
              </v-chip>
            </template>
            <template #item.actions="{ item }">
              <v-btn icon="mdi-delete" size="x-small" variant="text" color="error"
                     @click="removeException(item)" />
            </template>
          </v-data-table>
        </v-card-text>
      </v-card>
      <v-card v-else>
        <v-card-text class="text-medium-emphasis">
          左のリストでカレンダーを選択してください
        </v-card-text>
      </v-card>
    </v-col>
  </v-row>

  <!-- New calendar dialog -->
  <v-dialog v-model="dialog" max-width="500">
    <v-card title="新規カレンダー">
      <v-card-text>
        <v-text-field v-model="form.code" label="コード (例: STD-5DAY)" />
        <v-text-field v-model="form.name" label="名称" />
        <v-checkbox v-model="form.isDefault" label="既定カレンダーにする" density="compact" />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="dialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="createCal">作成</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Exception dialog -->
  <v-dialog v-model="exDialog" max-width="500">
    <v-card title="例外日を登録">
      <v-card-text>
        <v-text-field v-model="exForm.exceptionDate" type="date" label="日付" />
        <v-select v-model="exForm.kind"
                  :items="[
                    {title:'休業 (HOLIDAY)', value:'HOLIDAY'},
                    {title:'特別出勤 (WORKDAY)', value:'WORKDAY'}
                  ]" label="種別" />
        <v-text-field v-if="exForm.kind === 'WORKDAY'"
                      v-model.number="exForm.minutes" type="number"
                      label="特別稼働分数" />
        <v-text-field v-model="exForm.description" label="備考 (例: 元日)" />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="exDialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="saveException">登録</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { CalendarApi, type CalendarException, type WorkCalendar } from '@/api'

const calendars = ref<WorkCalendar[]>([])
const selected = ref<WorkCalendar | null>(null)
const exceptions = ref<CalendarException[]>([])
const busy = ref(false)

const dialog = ref(false)
const form = ref<WorkCalendar>(blank())

const exDialog = ref(false)
const exForm = ref<CalendarException>({
  calendarId: '', exceptionDate: '', kind: 'HOLIDAY', minutes: 0, description: ''
})

const days = [
  { label: '月', field: 'mondayMin' },    { label: '火', field: 'tuesdayMin' },
  { label: '水', field: 'wednesdayMin' }, { label: '木', field: 'thursdayMin' },
  { label: '金', field: 'fridayMin' },    { label: '土', field: 'saturdayMin' },
  { label: '日', field: 'sundayMin' }
]
const exHeaders = [
  { title: '日付',     key: 'exceptionDate' },
  { title: '種別',     key: 'kind' },
  { title: '分数',     key: 'minutes', align: 'end' as const },
  { title: '備考',     key: 'description' },
  { title: '',         key: 'actions', sortable: false, align: 'end' as const }
]

function blank(): WorkCalendar {
  return {
    code: '', name: '', isDefault: false,
    mondayMin: 480, tuesdayMin: 480, wednesdayMin: 480, thursdayMin: 480,
    fridayMin: 480, saturdayMin: 0, sundayMin: 0
  }
}

async function load() { calendars.value = await CalendarApi.list() }
onMounted(async () => {
  await load()
  if ((calendars.value ?? []).length) await select(calendars.value[0])
})

async function select(c: WorkCalendar) {
  selected.value = { ...c }
  if (c.id) exceptions.value = await CalendarApi.exceptions(c.id)
}

function openNew() { form.value = blank(); dialog.value = true }
async function createCal() {
  await CalendarApi.create(form.value)
  dialog.value = false
  await load()
}
async function saveCal() {
  if (!selected.value?.id) return
  busy.value = true
  try {
    await CalendarApi.update(selected.value.id, selected.value)
    await load()
  } finally { busy.value = false }
}
async function removeCal() {
  if (!selected.value?.id) return
  if (!confirm(`${selected.value.name} を削除しますか？`)) return
  await CalendarApi.remove(selected.value.id)
  selected.value = null
  exceptions.value = []
  await load()
}

function openException() {
  if (!selected.value?.id) return
  exForm.value = {
    calendarId: selected.value.id,
    exceptionDate: new Date().toISOString().slice(0, 10),
    kind: 'HOLIDAY', minutes: 0, description: ''
  }
  exDialog.value = true
}
async function saveException() {
  if (!selected.value?.id) return
  await CalendarApi.addException(selected.value.id, exForm.value)
  exDialog.value = false
  exceptions.value = (await CalendarApi.exceptions(selected.value.id)) ?? []
}
async function removeException(e: CalendarException) {
  if (!e.id || !selected.value?.id) return
  await CalendarApi.removeException(e.id)
  exceptions.value = (await CalendarApi.exceptions(selected.value.id)) ?? []
}

function fmt(d: string) { return d ? new Date(d).toLocaleDateString('ja-JP') : '' }
</script>
