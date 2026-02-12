<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore } from '@/stores'
import { opsAPI, type OpsErrorDetail, type OpsRequestDebugBundle, type OpsErrorLog, type OpsRequestDump } from '@/api/admin/ops'
import { formatDateTime } from '../utils/opsFormatters'

interface Props {
  show: boolean
  requestKey: string | null
}

const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
}>()

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const loading = ref(false)
const bundle = ref<OpsRequestDebugBundle | null>(null)

const dumpLoading = ref(false)
const dump = ref<OpsRequestDump | null>(null)

const selectedErrorId = ref<number | null>(null)
const selectedError = ref<OpsErrorDetail | null>(null)
const selectedErrorLoading = ref(false)

const keyLabel = computed(() => (props.requestKey || '').trim())

function close() {
  emit('update:show', false)
}

async function handleCopyKey() {
  const key = keyLabel.value
  if (!key) return
  const ok = await copyToClipboard(key, t('admin.ops.requestDetails.requestIdCopied'))
  if (!ok) appStore.showWarning(t('admin.ops.requestDetails.copyFailed'))
}

function prettyJSON(raw?: string): string {
  if (!raw) return 'N/A'
  try {
    const obj = JSON.parse(raw)
    return JSON.stringify(obj, null, 2)
  } catch {
    return String(raw)
  }
}

function prettyObject(obj: any): string {
  if (!obj) return 'N/A'
  try {
    return JSON.stringify(obj, null, 2)
  } catch {
    return String(obj)
  }
}

async function fetchBundle() {
  const key = keyLabel.value
  if (!props.show || !key) return
  loading.value = true
  try {
    bundle.value = await opsAPI.getRequestDebugBundle(key, { limit: 100 })
  } catch (e: any) {
    console.error('[OpsRequestDebugDrawer] Failed to fetch request debug bundle', e)
    appStore.showError(e?.message || t('admin.ops.requestDetails.failedToLoad'))
    bundle.value = null
  } finally {
    loading.value = false
  }
}

async function fetchDump() {
  const key = keyLabel.value
  if (!props.show || !key) return
  dumpLoading.value = true
  try {
    const res = await opsAPI.getRequestDump(key)
    dump.value = res?.dump || null
  } catch (e: any) {
    console.error('[OpsRequestDebugDrawer] Failed to fetch request dump', e)
    dump.value = null
  } finally {
    dumpLoading.value = false
  }
}

async function loadErrorDetail(id: number) {
  selectedErrorId.value = id
  selectedError.value = null
  selectedErrorLoading.value = true
  try {
    selectedError.value = await opsAPI.getErrorLogDetail(id)
  } catch (e: any) {
    console.error('[OpsRequestDebugDrawer] Failed to fetch error detail', e)
    appStore.showError(e?.message || t('admin.ops.errorDetail.loading'))
    selectedError.value = null
  } finally {
    selectedErrorLoading.value = false
  }
}

const errorRows = computed<OpsErrorLog[]>(() => bundle.value?.error_logs || [])
const usageRows = computed(() => bundle.value?.usage_logs || [])

const usageSummary = computed(() => {
  const first = usageRows.value[0]
  if (!first) return null
  const input = first.input_tokens || 0
  const output = first.output_tokens || 0
  const cacheCreation = first.cache_creation_tokens || 0
  const cacheRead = first.cache_read_tokens || 0
  const cache5m = (first as any).cache_creation_5m_tokens || 0
  const cache1h = (first as any).cache_creation_1h_tokens || 0
  const total = input + output + cacheCreation + cacheRead + cache5m + cache1h
  return { input, output, cacheCreation, cacheRead, cache5m, cache1h, total, first }
})

watch(
  () => props.show,
  (open) => {
    if (!open) {
      bundle.value = null
      dump.value = null
      selectedErrorId.value = null
      selectedError.value = null
      return
    }
    fetchBundle()
    fetchDump()
  }
)

watch(
  () => props.requestKey,
  () => {
    if (!props.show) return
    bundle.value = null
    dump.value = null
    selectedErrorId.value = null
    selectedError.value = null
    fetchBundle()
    fetchDump()
  }
)
</script>

<template>
  <BaseDialog
    :show="show"
    :title="t('admin.ops.requestDetails.details')"
    width="full"
    :z-index="60"
    :close-on-click-outside="true"
    @close="close"
  >
    <div class="space-y-6">
      <div class="flex flex-wrap items-center gap-2 rounded-xl border border-gray-200 bg-gray-50 p-3 text-xs dark:border-dark-700 dark:bg-dark-800/40">
        <div class="font-bold text-gray-500 dark:text-gray-400">{{ t('admin.ops.requestDetails.table.requestId') }}:</div>
        <div class="break-all font-mono text-xs font-semibold text-gray-900 dark:text-white">{{ keyLabel || '—' }}</div>
        <button
          v-if="keyLabel"
          type="button"
          class="ml-auto rounded-md bg-white px-2 py-1 text-[10px] font-bold text-gray-600 ring-1 ring-inset ring-gray-200 hover:bg-gray-50 dark:bg-dark-900 dark:text-gray-300 dark:ring-dark-700 dark:hover:bg-dark-800"
          @click="handleCopyKey"
        >
          {{ t('admin.ops.requestDetails.copy') }}
        </button>
      </div>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <div class="flex items-center gap-3 text-sm text-gray-500 dark:text-gray-400">
          <div class="h-5 w-5 animate-spin rounded-full border-b-2 border-primary-600"></div>
          {{ t('common.loading') }}
        </div>
      </div>

      <div v-else class="space-y-6">
        <!-- Request dump -->
        <div class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/40">
          <div class="flex items-center justify-between gap-3">
            <div class="text-xs font-black uppercase tracking-wider text-gray-500 dark:text-gray-400">REQUEST DUMP</div>
            <div v-if="dumpLoading" class="text-xs text-gray-400">{{ t('common.loading') }}</div>
          </div>

          <div v-if="!dumpLoading && !dump" class="mt-2 text-sm text-gray-500 dark:text-gray-400">
            {{ t('common.noData') }}
          </div>

          <div v-else-if="dump" class="mt-3 space-y-3">
            <div class="grid grid-cols-2 gap-3 text-xs text-gray-600 dark:text-gray-300">
              <div>
                <span class="text-gray-400">Type:</span>
                <span class="ml-1 font-mono">{{ dump.dump_type || '-' }}</span>
              </div>
              <div>
                <span class="text-gray-400">Status:</span>
                <span class="ml-1 font-mono">{{ dump.status_code ?? 0 }}</span>
              </div>
              <div class="col-span-2">
                <span class="text-gray-400">Path:</span>
                <span class="ml-1 font-mono break-all">{{ dump.request_path || '-' }}</span>
              </div>
              <div class="col-span-2">
                <span class="text-gray-400">Time:</span>
                <span class="ml-1 font-mono">{{ formatDateTime(dump.created_at) }}</span>
              </div>
            </div>

            <div>
              <div class="mb-1 text-[10px] font-bold text-gray-400">CLIENT HEADERS</div>
              <pre class="max-h-[220px] overflow-auto rounded-xl border border-gray-200 bg-white p-3 text-xs text-gray-800 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-100"><code>{{ prettyObject(dump.request_headers) }}</code></pre>
            </div>

            <div>
              <div class="mb-1 flex items-center justify-between gap-2 text-[10px] font-bold text-gray-400">
                <span>CLIENT BODY</span>
                <span class="font-mono">{{ dump.request_body_bytes || 0 }} B</span>
              </div>
              <pre class="max-h-[320px] overflow-auto rounded-xl border border-gray-200 bg-white p-3 text-xs text-gray-800 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-100"><code>{{ dump.request_body || 'N/A' }}</code></pre>
            </div>

            <div>
              <div class="mb-1 flex items-center justify-between gap-2 text-[10px] font-bold text-gray-400">
                <span>UPSTREAM BODY</span>
                <span class="font-mono">{{ dump.upstream_request_body_bytes || 0 }} B</span>
              </div>
              <pre class="max-h-[320px] overflow-auto rounded-xl border border-gray-200 bg-white p-3 text-xs text-gray-800 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-100"><code>{{ dump.upstream_request_body || 'N/A' }}</code></pre>
            </div>
          </div>
        </div>

        <!-- Usage summary -->
        <div class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/40">
          <div class="text-xs font-black uppercase tracking-wider text-gray-500 dark:text-gray-400">
            {{ t('admin.ops.requestDetails.kind.success') }}
          </div>

          <div v-if="!usageSummary" class="mt-2 text-sm text-gray-500 dark:text-gray-400">
            {{ t('common.noData') }}
          </div>

          <div v-else class="mt-3 grid grid-cols-2 gap-3 sm:grid-cols-3">
            <div class="rounded-lg bg-white p-3 dark:bg-dark-900">
              <div class="text-[10px] font-bold text-gray-400">TOTAL</div>
              <div class="mt-1 font-mono text-sm font-semibold text-gray-900 dark:text-white">{{ usageSummary.total }}</div>
            </div>
            <div class="rounded-lg bg-white p-3 dark:bg-dark-900">
              <div class="text-[10px] font-bold text-gray-400">INPUT</div>
              <div class="mt-1 font-mono text-sm font-semibold text-gray-900 dark:text-white">{{ usageSummary.input }}</div>
            </div>
            <div class="rounded-lg bg-white p-3 dark:bg-dark-900">
              <div class="text-[10px] font-bold text-gray-400">OUTPUT</div>
              <div class="mt-1 font-mono text-sm font-semibold text-gray-900 dark:text-white">{{ usageSummary.output }}</div>
            </div>
            <div class="rounded-lg bg-white p-3 dark:bg-dark-900">
              <div class="text-[10px] font-bold text-gray-400">TTFT</div>
              <div class="mt-1 font-mono text-sm font-semibold text-gray-900 dark:text-white">
                {{ typeof usageSummary.first.first_token_ms === 'number' ? `${usageSummary.first.first_token_ms} ms` : '—' }}
              </div>
            </div>
            <div class="rounded-lg bg-white p-3 dark:bg-dark-900">
              <div class="text-[10px] font-bold text-gray-400">DURATION</div>
              <div class="mt-1 font-mono text-sm font-semibold text-gray-900 dark:text-white">
                {{ typeof usageSummary.first.duration_ms === 'number' ? `${usageSummary.first.duration_ms} ms` : '—' }}
              </div>
            </div>
            <div class="rounded-lg bg-white p-3 dark:bg-dark-900">
              <div class="text-[10px] font-bold text-gray-400">MODEL</div>
              <div class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white" :title="usageSummary.first.model || ''">
                {{ usageSummary.first.model || '—' }}
              </div>
            </div>
          </div>
        </div>

        <!-- Errors -->
        <div class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/40">
          <div class="flex items-center justify-between gap-3">
            <div class="text-xs font-black uppercase tracking-wider text-gray-500 dark:text-gray-400">
              {{ t('admin.ops.requestDetails.kind.error') }}
            </div>
            <div class="text-xs text-gray-400">{{ errorRows.length }}</div>
          </div>

          <div v-if="!errorRows.length" class="mt-2 text-sm text-gray-500 dark:text-gray-400">
            {{ t('common.noData') }}
          </div>

          <div v-else class="mt-3 space-y-2">
            <button
              v-for="ev in errorRows"
              :key="ev.id"
              type="button"
              class="w-full rounded-lg border border-gray-200 bg-white p-3 text-left hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-900 dark:hover:bg-dark-800"
              @click="loadErrorDetail(ev.id)"
            >
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="rounded-md bg-gray-100 px-2 py-0.5 font-mono text-[10px] font-bold text-gray-700 dark:bg-dark-700 dark:text-gray-200">
                      {{ ev.status_code ?? 0 }}
                    </span>
                    <span class="truncate text-xs font-semibold text-gray-900 dark:text-white" :title="ev.message">
                      {{ ev.message || '—' }}
                    </span>
                  </div>
                  <div class="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-gray-500 dark:text-gray-400">
                    <span class="font-mono">{{ ev.type }}</span>
                    <span>·</span>
                    <span>{{ formatDateTime(ev.created_at) }}</span>
                  </div>
                </div>
                <Icon name="chevronRight" size="xs" :stroke-width="2" class="mt-1 text-gray-400" />
              </div>
            </button>
          </div>

          <!-- Selected error detail -->
          <div v-if="selectedErrorId" class="mt-4 rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900">
            <div class="flex items-center justify-between gap-3">
              <div class="text-xs font-black uppercase tracking-wider text-gray-500 dark:text-gray-400">
                {{ t('admin.ops.errorDetail.titleWithId', { id: String(selectedErrorId) }) }}
              </div>
              <div v-if="selectedErrorLoading" class="text-xs text-gray-400">{{ t('common.loading') }}</div>
            </div>

            <div v-if="selectedError" class="mt-3 space-y-3">
              <div class="grid grid-cols-2 gap-3 text-xs text-gray-600 dark:text-gray-300">
                <div>
                  <span class="text-gray-400">Type:</span>
                  <span class="ml-1 font-mono">{{ selectedError.type }}</span>
                </div>
                <div>
                  <span class="text-gray-400">Phase:</span>
                  <span class="ml-1 font-mono">{{ selectedError.phase }}</span>
                </div>
                <div>
                  <span class="text-gray-400">Owner:</span>
                  <span class="ml-1 font-mono">{{ selectedError.error_owner }}</span>
                </div>
                <div>
                  <span class="text-gray-400">Source:</span>
                  <span class="ml-1 font-mono">{{ selectedError.error_source }}</span>
                </div>
              </div>

              <pre class="max-h-[360px] overflow-auto rounded-xl border border-gray-200 bg-gray-50 p-3 text-xs text-gray-800 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-100"><code>{{ prettyJSON(selectedError.error_body || selectedError.upstream_error_detail || selectedError.upstream_errors || selectedError.upstream_error_message || '') }}</code></pre>
            </div>
          </div>
        </div>
      </div>
    </div>
  </BaseDialog>
</template>

