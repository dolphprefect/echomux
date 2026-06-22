<script>
  import { onMount, onDestroy } from 'svelte'
  import { api } from './lib/api.js'
  import DeviceCard from './components/DeviceCard.svelte'
  import ScanSheet from './components/ScanSheet.svelte'
  import DelaySheet from './components/DelaySheet.svelte'

  let devices = []
  let loadError = false
  let reconnecting = false
  let scanOpen = false
  let delayDevice = null
  let connecting = new Set()
  let connectErrors = {} // MAC → transient error string
  let restarting = false
  let destroyed = false

  async function load() {
    try {
      const fresh = await api('GET', '/devices')
      // The API returns lowercase "muted" and "playing" (json: tags in Go);
      // components and WS event handlers read capital-case Muted / Playing.
      // Normalise here so every poll keeps the display in sync.
      devices = fresh.map(d => {
        const n = { ...d, Muted: d.muted, Playing: d.playing }
        return connecting.has(n.MAC) ? { ...n, Connected: true } : n
      })
      loadError = false
    } catch(e) {
      loadError = true
    }
  }

  let pollInterval, ws

  function connectWS() {
    if (destroyed) return
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    ws = new WebSocket(`${proto}//${location.host}/events`)
    ws.onmessage = e => {
      try {
        const ev = JSON.parse(e.data)
        const dev = devices.find(d => d.MAC === ev.mac)
        if (!dev) return
        if (ev.type === 'connected' || ev.type === 'disconnected') {
          dev.Connected = ev.type === 'connected'
          if (!dev.Connected) dev.Playing = false
        } else if (ev.type === 'loopback_started') {
          dev.Playing = true
        } else if (ev.type === 'loopback_stopped') {
          dev.Playing = false
        }
        devices = devices
      } catch(e) {}
    }
    ws.onclose = () => { if (!destroyed) setTimeout(connectWS, 3000) }
  }

  onMount(() => {
    load()
    pollInterval = setInterval(load, 5000)
    connectWS()
  })

  onDestroy(() => {
    destroyed = true
    if (ws) ws.onclose = null
    ws?.close()
    clearInterval(pollInterval)
  })

  async function doConnect(mac) {
    connecting.add(mac)
    connecting = connecting
    const { [mac]: _, ...rest } = connectErrors
    connectErrors = rest  // clear any previous error for this MAC
    try {
      await api('POST', `/devices/${mac}/connect`)
    } catch(e) {
      const msg = e.message.includes('org.bluez') ? 'Connection failed' : (e.message || 'Connection failed')
      connectErrors = { ...connectErrors, [mac]: msg }
      setTimeout(() => {
        const { [mac]: _, ...cleared } = connectErrors
        connectErrors = cleared
      }, 5000)
    } finally {
      connecting.delete(mac)
      connecting = connecting
    }
  }

  async function doDisconnect(mac) {
    const dev = devices.find(d => d.MAC === mac)
    if (dev) { dev.Connected = false; dev.Playing = false; devices = devices }
    try {
      await api('POST', `/devices/${mac}/disconnect`)
    } catch(e) {
      if (dev) { dev.Connected = true; devices = devices }
    }
  }

  async function doForget(mac) {
    const dev = devices.find(d => d.MAC === mac)
    if (!confirm(`Forget ${dev?.Name || mac}?\nThis will unpair the speaker from this Pi.`)) return
    try {
      await api('DELETE', `/devices/${mac}`)
      devices = devices.filter(d => d.MAC !== mac)
    } catch(e) {}
  }

  function handleVolumeChange(e) {
    const { mac, level } = e.detail
    const dev = devices.find(d => d.MAC === mac)
    if (dev) { dev.volume = level; devices = devices }
  }

  function handleMuteChange(e) {
    const { mac, muted } = e.detail
    const dev = devices.find(d => d.MAC === mac)
    if (dev) { dev.Muted = muted; devices = devices }
  }

  async function restartAudio() {
    restarting = true
    try {
      await api('POST', '/playback/restart')
      await new Promise(r => setTimeout(r, 2500))
      await load()
    } catch(e) {}
    restarting = false
  }

  async function handleScanClose(e) {
    scanOpen = false
    const prev = e.detail.prevConnected || []
    reconnecting = true
    try {
      await api('POST', '/playback/resume')
      for (const d of prev) {
        try { await api('POST', `/devices/${d.MAC}/connect`) } catch(e) {}
      }
    } finally {
      reconnecting = false
      await load()
    }
  }

  $: sorted = [...devices].sort((a, b) => {
    if (a.Connected !== b.Connected) return a.Connected ? -1 : 1
    return a.Name.localeCompare(b.Name)
  })

  $: knownMACs = new Set(devices.map(d => d.MAC))
</script>

<header>
  <div class="wordmark">
    <img src="/icon.svg" alt="" class="wordmark-icon">
    <span><b>echo</b><span class="sep">·</span>mux</span>
  </div>
  <div style="display:flex;align-items:center;gap:6px">
    <button
      class="btn-restart"
      class:spinning={restarting}
      disabled={restarting || reconnecting}
      title="Restart audio loopbacks"
      on:click={restartAudio}
    >
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="23 4 23 10 17 10"/>
        <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
      </svg>
    </button>
    <button
      class="btn-add"
      disabled={reconnecting}
      title={reconnecting ? 'Reconnecting speakers…' : 'Add speaker'}
      on:click={() => { if (!reconnecting) scanOpen = true }}
    >
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round">
        <line x1="8" y1="2" x2="8" y2="14"/><line x1="2" y1="8" x2="14" y2="8"/>
      </svg>
    </button>
  </div>
</header>

<div id="device-list">
  {#if loadError}
    <p class="empty">
      <strong>Can't reach echomux</strong>
      Check your connection.<br><br>
      <button class="btn-connect" on:click={load}>Try again</button>
    </p>
  {:else if devices.length === 0 && !loadError}
    <p class="empty"><strong>No speakers yet</strong>Tap + to add one.</p>
  {:else}
    {#each sorted as device (device.MAC)}
      <DeviceCard
        {device}
        isConnecting={connecting.has(device.MAC)}
        connectError={connectErrors[device.MAC] || null}
        on:connect={e => doConnect(e.detail)}
        on:disconnect={e => doDisconnect(e.detail)}
        on:forget={e => doForget(e.detail)}
        on:openDelay={e => delayDevice = e.detail}
        on:volumeChange={handleVolumeChange}
        on:muteChange={handleMuteChange}
      />
    {/each}
  {/if}
</div>

{#if scanOpen}
  <ScanSheet {knownMACs} on:close={handleScanClose} />
{/if}

{#if delayDevice}
  <DelaySheet
    device={delayDevice}
    on:close={() => delayDevice = null}
    on:updated={e => {
      const dev = devices.find(d => d.MAC === e.detail.mac)
      if (dev) { dev.delay_ms = e.detail.ms; devices = devices }
    }}
  />
{/if}
