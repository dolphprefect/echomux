<script>
  import { createEventDispatcher } from 'svelte'
  import DeviceCard from './DeviceCard.svelte'

  export let node
  export let devices = []
  export let connecting
  export let connectErrors
  export let scanningNodeId = null
  export let reconnecting = false

  const dispatch = createEventDispatcher()

  $: isMaster = node.role === 'master'
  $: headerText = isMaster ? `MASTER · ${node.name}` : `SATELLITE · ${node.name}`
  $: isOffline = !node.online
  $: isScanning = scanningNodeId === node.id
  $: isThrottleActive = scanningNodeId === node.id

  $: sorted = [...devices].sort((a, b) => {
    if (a.Connected !== b.Connected) return a.Connected ? -1 : 1
    return a.Name.localeCompare(b.Name)
  })
</script>

<div class="node-section" class:offline={isOffline}>
  <div class="node-header">
    <div class="node-title">
      <span class="role-badge" class:master={isMaster}>{isMaster ? 'MASTER' : 'SATELLITE'}</span>
      <span class="node-name">{node.name}</span>
      {#if isOffline}
        <span class="offline-badge">OFFLINE</span>
      {/if}
    </div>
    {#if !isOffline}
      <div class="node-actions">
        <button
          class="btn-node-restart"
          title="Restart audio loopbacks for this node"
          on:click={() => dispatch('restart', node.id)}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="23 4 23 10 17 10"/>
            <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
          </svg>
        </button>
        <button
          class="btn-node-add"
          disabled={scanningNodeId !== null || reconnecting}
          title={isScanning ? 'Scanning…' : 'Add speaker'}
          on:click={() => dispatch('scan', node.id)}
        >
          {#if isScanning}
            <span class="spinner-sm"></span>
          {:else}
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round">
              <line x1="8" y1="2" x2="8" y2="14"/><line x1="2" y1="8" x2="14" y2="8"/>
            </svg>
          {/if}
        </button>
      </div>
    {/if}
  </div>

  <div class="node-devices" class:throttled={isThrottleActive}>
    {#if isOffline}
      <p class="empty-node">Node is offline.</p>
    {:else}
      {#each sorted as device (device.MAC)}
        <DeviceCard
          {device}
          isConnecting={connecting.has(device.MAC)}
          connectError={connectErrors[device.MAC] || null}
          disabled={isThrottleActive}
          on:connect
          on:disconnect
          on:forget
          on:openDelay
          on:volumeChange
          on:muteChange
        />
      {:else}
        <p class="empty-node">No speakers yet.</p>
      {/each}
    {/if}
  </div>
</div>

<style>
  .node-section {
    margin-bottom: 24px;
    border-radius: 12px;
    background: var(--card-bg, #1a1a1a);
    overflow: hidden;
    border: 1px solid var(--border, #2a2a2a);
  }
  .node-section.offline {
    opacity: 0.6;
    border-style: dashed;
  }
  .node-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 12px 16px;
    background: rgba(255, 255, 255, 0.02);
    border-bottom: 1px solid var(--border, #2a2a2a);
  }
  .node-title {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 0.95rem;
    font-weight: 600;
  }
  .role-badge {
    font-size: 0.7rem;
    font-weight: 700;
    padding: 2px 6px;
    border-radius: 4px;
    background: #333;
    color: #ccc;
  }
  .role-badge.master {
    background: rgba(230, 185, 30, 0.15);
    color: var(--gold, #e6b91e);
  }
  .node-name {
    color: var(--fg, #fff);
  }
  .offline-badge {
    font-size: 0.7rem;
    font-weight: 700;
    padding: 2px 6px;
    border-radius: 4px;
    background: #600;
    color: #ffcdd2;
  }
  .node-actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .btn-node-restart, .btn-node-add {
    background: none;
    border: none;
    color: var(--sub, #888);
    cursor: pointer;
    padding: 6px;
    border-radius: 6px;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: background 0.15s, color 0.15s;
  }
  .btn-node-restart:hover, .btn-node-add:hover {
    background: var(--border, #2a2a2a);
    color: var(--fg, #fff);
  }
  .btn-node-restart svg, .btn-node-add svg {
    width: 16px;
    height: 16px;
  }
  .btn-node-add:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .node-devices {
    padding: 16px;
    display: grid;
    grid-template-columns: 1fr;
    gap: 16px;
  }
  @media(min-width: 600px) {
    .node-devices {
      grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    }
  }
  .node-devices.throttled {
    pointer-events: none;
    opacity: 0.65;
  }
  .empty-node {
    grid-column: 1 / -1;
    text-align: center;
    color: var(--sub, #888);
    font-size: 0.9rem;
    padding: 16px 0;
    margin: 0;
  }
  .spinner-sm {
    display: inline-block;
    width: 14px;
    height: 14px;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-radius: 50%;
    animation: spin 0.75s linear infinite;
  }
  @keyframes spin {
    to { transform: rotate(360deg); }
  }
</style>
