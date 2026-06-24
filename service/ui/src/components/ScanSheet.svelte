<script>
  import { createEventDispatcher, onMount } from 'svelte'
  import { api } from '../lib/api.js'

  export let knownMACs = new Set()
  export let nodeId = null

  const dispatch = createEventDispatcher()

  let busy = true
  let results = []
  let scanError = null
  let adding = {}      // MAC → 'loading' | 'done' | 'error'
  let prevConnected = []

  onMount(async () => {
    // Capture who was connected before the scan pauses them.
    try {
      const devs = await api('GET', '/devices', undefined, nodeId)
      prevConnected = devs.filter(d => d.Connected)
    } catch(e) {}
    await startScan()
  })

  async function startScan() {
    busy = true
    results = []
    scanError = null
    adding = {}
    try {
      const resp = await api('POST', '/scan', { timeout_sec: 10 }, nodeId)
      if (resp.error) {
        scanError = resp.error
      } else {
        results = (resp.devices || []).filter(d => !knownMACs.has(d.MAC))
      }
    } catch(e) {
      scanError = e.message || 'Scan failed'
    }
    busy = false
  }

  async function addDevice(mac) {
    adding = { ...adding, [mac]: 'loading' }
    try {
      await api('POST', `/devices/${mac}/pair`, undefined, nodeId)
      await api('POST', `/devices/${mac}/connect`, undefined, nodeId)
      const updated = { ...adding, [mac]: 'done' }
      adding = updated
      // Only auto-close when no other adds are still in-flight.
      if (!Object.values(updated).some(s => s === 'loading')) setTimeout(close, 800)
    } catch(e) {
      adding = { ...adding, [mac]: 'error' }
    }
  }

  function close() {
    dispatch('close', { prevConnected })
  }
</script>

<div class="sheet-overlay open">
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="sheet-backdrop" on:click={close}></div>
  <div class="sheet">
    <div class="sheet-header">
      <span class="sheet-title">Add speaker</span>
      <button class="btn-close" on:click={close}>
        <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <line x1="2" y1="2" x2="12" y2="12"/><line x1="12" y1="2" x2="2" y2="12"/>
        </svg>
      </button>
    </div>

    {#if busy}
      <div class="scan-status">
        <div class="spinner"></div>
        Scanning&hellip;
      </div>
    {:else}
      {#if scanError}
        <p class="scan-status scan-error">
          Scan failed: {scanError}
        </p>
      {:else if results.length === 0}
        <p class="scan-status">
          No new speakers found.<br>Make sure it's in pairing mode.
        </p>
      {:else}
        {#each results as d (d.MAC)}
          {@const st = adding[d.MAC] || ''}
          <div class="scan-item">
            <div>
              <div class="scan-item-name">{d.Name || 'Unknown'}</div>
              <div class="scan-item-mac">{d.MAC}</div>
            </div>
            <button
              class="btn-scan-add {st === 'done' ? 'btn-scan-done' : ''}"
              disabled={st === 'loading' || st === 'done'}
              on:click={() => addDevice(d.MAC)}
            >
              {#if st === 'loading'}Adding&hellip;
              {:else if st === 'done'}&#10003; Added
              {:else}Add{/if}
            </button>
          </div>
        {/each}
      {/if}

      <div style="text-align:center;padding:14px 0 2px">
        <button class="btn-scan-add" on:click={startScan} style="padding:7px 22px">
          Scan again
        </button>
      </div>
    {/if}
  </div>
</div>
