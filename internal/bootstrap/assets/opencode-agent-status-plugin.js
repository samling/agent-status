const EVENT_MAP = {
  "chat.message": "UserPromptSubmit",
  "tool.execute.before": "PreToolUse",
  "tool.execute.after": "PostToolUse",
  "permission.ask": "PermissionRequest",
  "session.created": "SessionStart",
}

const STOP_EVENTS = new Set(["session.idle", "session.completed", "session.stopped", "session.finished"])

async function fileExists(path) {
  try {
    const fs = await import("node:fs/promises")
    await fs.access(path)
    return true
  } catch {
    return false
  }
}

async function readEndpointFile() {
  const os = await import("node:os")
  const path = await import("node:path")
  const fs = await import("node:fs/promises")
  const stateHome = process.env.XDG_STATE_HOME || path.join(os.homedir(), ".local", "state")
  const endpointFile = path.join(stateHome, "agent-status", "endpoint")
  if (!(await fileExists(endpointFile))) {
    return ""
  }
  return (await fs.readFile(endpointFile, "utf8")).trim()
}

async function resolveEndpoint() {
  if (process.env.AGENT_STATUS_ENDPOINT) {
    return process.env.AGENT_STATUS_ENDPOINT
  }
  const endpoint = await readEndpointFile()
  if (endpoint) {
    return endpoint
  }
  return "http://127.0.0.1:7878"
}

function normalizeEndpoint(value) {
  const trimmed = String(value || "").trim()
  if (/^https?:\/\//i.test(trimmed)) {
    return trimmed
  }
  return `http://${trimmed}`
}

function eventType(input) {
  if (typeof input?.event === "string") {
    return input.event
  }
  return input?.type || input?.name || input?.event?.type || ""
}

function hookName(event) {
  const name = eventType(event)
  if (name === "chat.message" || name === "message.updated") {
    const role = messageRole(event)
    if (role === "assistant" && messageComplete(event)) {
      return "Stop"
    }
    if (role === "user") {
      return "UserPromptSubmit"
    }
    return name === "chat.message" ? "UserPromptSubmit" : ""
  }
  if (EVENT_MAP[name]) {
    return EVENT_MAP[name]
  }
  if (STOP_EVENTS.has(name) && sessionID(event)) {
    return "Stop"
  }
  if (name?.startsWith("message.")) {
    return "UserPromptSubmit"
  }
  return name || "Event"
}

function withType(input, type) {
  return { ...(input || {}), type }
}

function sessionID(input) {
  return input?.sessionID || input?.event?.properties?.sessionID || input?.event?.properties?.info?.id || input?.event?.properties?.info?.sessionID || input?.event?.sessionID || input?.message?.sessionID || ""
}

function turnID(input) {
  return input?.messageID || input?.callID || ""
}

function messageRole(input) {
  return input?.message?.role || input?.event?.properties?.message?.role || input?.event?.properties?.info?.role || ""
}

function messageComplete(input) {
  const message = input?.message || input?.event?.properties?.message || input?.event?.properties?.info || {}
  return Boolean(message?.finish || message?.time?.completed || message?.completed || message?.time_completed)
}

function toolName(input) {
  if (typeof input?.tool === "string") {
    return input.tool
  }
  const tool = input?.tool || input?.event?.properties?.tool || input?.event?.properties?.info?.tool || {}
  if (typeof tool === "string") {
    return tool
  }
  return tool?.name || ""
}

async function postHook(event) {
  try {
    const session = sessionID(event)
    const eventName = hookName(event)
    if (!session || !eventName) {
      return
    }
    const endpoint = await resolveEndpoint()
    await fetch(new URL("/hook", normalizeEndpoint(endpoint)), {
      method: "POST",
      signal: AbortSignal.timeout(1000),
      headers: {
        "Content-Type": "application/json",
        "X-Agent": "opencode",
      },
      body: JSON.stringify({
        session_id: session,
        hook_event_name: eventName,
        turn_id: turnID(event),
        tool_name: toolName(event),
        payload: event || {},
      }),
    })
  } catch {
    // The collector is optional; opencode should continue if it is offline.
  }
}

export default async function agentStatusPlugin() {
  return {
    async event(input) {
      const name = hookName(input)
      if (name === "Stop" || name === "SessionStart" || EVENT_MAP[eventType(input)]) {
        await postHook(input)
      }
    },
    "chat.message": async function (input) {
      await postHook(withType(input, "chat.message"))
    },
    "tool.execute.before": async function (input) {
      await postHook(withType(input, "tool.execute.before"))
    },
    "tool.execute.after": async function (input) {
      await postHook(withType(input, "tool.execute.after"))
    },
    "permission.ask": async function (input) {
      await postHook(withType(input, "permission.ask"))
    },
  }
}
