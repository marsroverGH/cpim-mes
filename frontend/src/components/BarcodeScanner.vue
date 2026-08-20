<template>
  <v-dialog v-model="open" max-width="500" @update:model-value="onDialogChange">
    <v-card>
      <v-card-title class="d-flex align-center">
        <v-icon icon="mdi-barcode-scan" class="mr-2" />
        バーコード/QR スキャン
        <v-spacer />
        <v-btn icon="mdi-close" variant="text" size="small" @click="close" />
      </v-card-title>
      <v-card-text>
        <p v-if="!supported" class="text-error">
          このブラウザはカメラに対応していません。HTTPS 環境でアクセスしてください。
        </p>
        <div v-else id="bc-scanner-region" class="scanner-region" />
        <v-alert v-if="errorMsg" type="error" variant="tonal" class="mt-2" density="compact">
          {{ errorMsg }}
        </v-alert>
        <p class="text-caption text-medium-emphasis mt-2">
          ヒント: カメラに品目コードのバーコードまたは QR を映してください。
          自動的に検出され、ダイアログを閉じてフォームに反映されます。
        </p>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="close">閉じる</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void
  (e: 'detected', code: string): void
}>()

const open = ref(props.modelValue)
const errorMsg = ref('')
const supported = ref(typeof navigator !== 'undefined' && !!navigator.mediaDevices?.getUserMedia)

let scanner: any = null

watch(() => props.modelValue, async (v) => {
  open.value = v
  if (v) {
    // Slight delay so DOM element exists
    await nextTickThenStart()
  } else {
    await stopScanner()
  }
})

watch(open, (v) => emit('update:modelValue', v))

async function nextTickThenStart() {
  await new Promise(r => setTimeout(r, 50))
  await startScanner()
}

async function startScanner() {
  if (!supported.value) return
  try {
    // Lazy import: keeps initial bundle smaller for non-scanner users
    const mod = await import('html5-qrcode')
    const Html5Qrcode = mod.Html5Qrcode
    scanner = new Html5Qrcode('bc-scanner-region')
    const config = { fps: 10, qrbox: { width: 240, height: 160 } }
    await scanner.start(
      { facingMode: 'environment' },
      config,
      (decoded: string) => {
        emit('detected', decoded)
        close()
      },
      () => { /* per-frame failures are normal; ignore */ }
    )
  } catch (e: any) {
    errorMsg.value = e?.message || 'カメラの起動に失敗しました'
  }
}

async function stopScanner() {
  if (scanner) {
    try { await scanner.stop() } catch (_) { /* idempotent */ }
    try { scanner.clear() } catch (_) { /* */ }
    scanner = null
  }
}

function close() {
  open.value = false
  emit('update:modelValue', false)
}
function onDialogChange(v: boolean) {
  if (!v) stopScanner()
}

onBeforeUnmount(stopScanner)
</script>

<style scoped>
.scanner-region {
  width: 100%;
  min-height: 280px;
  background: #000;
  border-radius: 6px;
  overflow: hidden;
}
</style>
