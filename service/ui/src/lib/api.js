export async function api(method, path, body) {
  const opts = { method, headers: {} }
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const r = await fetch(path, opts)
  if (!r.ok) {
    const text = await r.text().catch(() => '')
    throw new Error(text.trim() || r.status)
  }
  const ct = r.headers.get('content-type') || ''
  if (ct.includes('json')) return r.json()
  return null
}
