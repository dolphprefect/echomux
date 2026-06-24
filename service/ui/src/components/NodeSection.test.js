import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import NodeSection from './NodeSection.svelte'

describe('NodeSection', () => {
  const nodeMaster = { id: 'living-room', name: 'Living Room', role: 'master', online: true }
  const nodeSat = { id: 'kitchen', name: 'Kitchen', role: 'satellite', online: true }
  const nodeOffline = { id: 'attic', name: 'Attic', role: 'satellite', online: false }

  const devices = [
    { MAC: '11:22', Name: 'Speaker A', Connected: true, node_id: 'kitchen', volume: 50 },
    { MAC: '33:44', Name: 'Speaker B', Connected: false, node_id: 'kitchen', volume: 50 }
  ]

  it('renders master node header correctly', () => {
    const { getByText } = render(NodeSection, {
      node: nodeMaster,
      devices: [],
      connecting: new Set(),
      connectErrors: {}
    })
    expect(getByText('MASTER')).toBeTruthy()
    expect(getByText('Living Room')).toBeTruthy()
  })

  it('renders satellite node header correctly', () => {
    const { getByText } = render(NodeSection, {
      node: nodeSat,
      devices: [],
      connecting: new Set(),
      connectErrors: {}
    })
    expect(getByText('SATELLITE')).toBeTruthy()
    expect(getByText('Kitchen')).toBeTruthy()
  })

  it('renders offline node correctly showing OFFLINE badge and no devices', () => {
    const { getByText, queryByText } = render(NodeSection, {
      node: nodeOffline,
      devices,
      connecting: new Set(),
      connectErrors: {}
    })
    expect(getByText('OFFLINE')).toBeTruthy()
    expect(getByText('Node is offline.')).toBeTruthy()
    expect(queryByText('Speaker A')).toBeNull()
  })

  it('dispatches scan event on add button click', async () => {
    const { container, component } = render(NodeSection, {
      node: nodeSat,
      devices: [],
      connecting: new Set(),
      connectErrors: {}
    })
    const handler = vi.fn()
    component.$on('scan', handler)

    await fireEvent.click(container.querySelector('.btn-node-add'))
    expect(handler).toHaveBeenCalled()
    expect(handler.mock.calls[0][0].detail).toBe('kitchen')
  })

  it('dispatches restart event on restart button click', async () => {
    const { container, component } = render(NodeSection, {
      node: nodeSat,
      devices: [],
      connecting: new Set(),
      connectErrors: {}
    })
    const handler = vi.fn()
    component.$on('restart', handler)

    await fireEvent.click(container.querySelector('.btn-node-restart'))
    expect(handler).toHaveBeenCalled()
    expect(handler.mock.calls[0][0].detail).toBe('kitchen')
  })

  it('disables add button on all sections when any node-scoped scan is active (scan lockout)', () => {
    // Current node is 'kitchen', but scanningNodeId is 'living-room'
    const { container } = render(NodeSection, {
      node: nodeSat,
      devices: [],
      connecting: new Set(),
      connectErrors: {},
      scanningNodeId: 'living-room'
    })
    const addButton = container.querySelector('.btn-node-add')
    expect(addButton.disabled).toBe(true)
  })

  it('applies throttled class and disables device cards only on the scanning node', () => {
    // 1. Current node is scanning: 'kitchen'
    const { container: containerScanning } = render(NodeSection, {
      node: nodeSat,
      devices,
      connecting: new Set(),
      connectErrors: {},
      scanningNodeId: 'kitchen'
    })
    const devicesDivScanning = containerScanning.querySelector('.node-devices')
    expect(devicesDivScanning.classList.contains('throttled')).toBe(true)
    const rangeInputScanning = containerScanning.querySelector('input[type="range"]')
    expect(rangeInputScanning.disabled).toBe(true)

    // 2. Current node is not scanning: 'kitchen' but 'living-room' is scanning
    const { container: containerNotScanning } = render(NodeSection, {
      node: nodeSat,
      devices,
      connecting: new Set(),
      connectErrors: {},
      scanningNodeId: 'living-room'
    })
    const devicesDivNotScanning = containerNotScanning.querySelector('.node-devices')
    expect(devicesDivNotScanning.classList.contains('throttled')).toBe(false)
    const rangeInputNotScanning = containerNotScanning.querySelector('input[type="range"]')
    expect(rangeInputNotScanning.disabled).toBe(false)
  })
})
