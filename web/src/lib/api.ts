export interface RegistryStatus {
  online: boolean
  registry: string
  apiVersion?: string
}

export interface AuthStatus {
  enabled: boolean
  authenticated: boolean
  user?: {
    subject: string
    name: string
    email?: string
    username?: string
  }
}

export interface Organization {
  name: string
  displayName: string
  description: string
  createdAt: string
  updatedAt: string
  repositoryCount: number
}

export interface Platform {
  architecture?: string
  os?: string
  variant?: string
}

export interface Image {
  tag: string
  digest: string
  mediaType: string
  size: number
  layers: number
  platforms?: Platform[]
  created?: string
  lastUsed?: string
  architecture?: string
  os?: string
}

export interface ImageGCCandidate {
  repository: string
  digest: string
  tags: string[]
  lastUsed: string
  logicalSize: number
}

export interface ImageGCPreview {
  planId: string
  generatedAt: string
  expiresAt: string
  cutoff: string
  candidates: ImageGCCandidate[]
  manifestCount: number
  tagCount: number
  logicalSize: number
}

export interface ImageGCResult {
  repository: string
  digest: string
  status: "deleted" | "skipped" | "failed"
  reason?: string
}

export interface ImageGCExecution {
  deletedCount: number
  skippedCount: number
  failedCount: number
  results: ImageGCResult[]
}

interface ApiErrorResponse {
  error?: string
}

export class ApiRequestError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
    this.name = "ApiRequestError"
  }
}

const repositoryPath = (repository: string) =>
  repository.split("/").map(encodeURIComponent).join("/")

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: {
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  })
  if (!response.ok) {
    if (response.status === 401 && path !== "/api/auth/me") {
      const returnPath = `${window.location.pathname}${window.location.search}`
      window.location.assign(`/auth/login?return=${encodeURIComponent(returnPath)}`)
    }
    const payload = (await response.json().catch(() => ({}))) as ApiErrorResponse
    throw new ApiRequestError(payload.error || `Request failed (${response.status})`, response.status)
  }
  if (response.status === 204) {
    return undefined as T
  }
  return response.json() as Promise<T>
}

export const api = {
  auth: (signal?: AbortSignal) => request<AuthStatus>("/api/auth/me", { signal }),
  logout: () => request<void>("/auth/logout", { method: "POST" }),
  status: (signal?: AbortSignal) => request<RegistryStatus>("/api/status", { signal }),
  organizations: (signal?: AbortSignal) =>
    request<{ organizations: Organization[] }>("/api/organizations", { signal }),
  createOrganization: (name: string, displayName: string, description: string) =>
    request<Organization>("/api/organizations", {
      method: "POST",
      body: JSON.stringify({ name, displayName, description }),
    }),
  updateOrganization: (name: string, displayName: string, description: string) =>
    request<Organization>(`/api/organizations/${encodeURIComponent(name)}`, {
      method: "PUT",
      body: JSON.stringify({ displayName, description }),
    }),
  deleteOrganization: (name: string) =>
    request<void>(`/api/organizations/${encodeURIComponent(name)}`, {
      method: "DELETE",
    }),
  repositories: (signal?: AbortSignal) =>
    request<{ repositories: string[] }>("/api/repositories", { signal }),
  imageBatch: (repositories: string[], signal?: AbortSignal) => {
    const query = new URLSearchParams()
    for (const repository of repositories) query.append("repository", repository)
    return request<{ repositories: { name: string; images: Image[] }[] }>(
      `/api/images?${query}`, { signal },
    )
  },
  images: (repository: string) =>
    request<{ name: string; images: Image[] }>(
      `/api/repositories/${repositoryPath(repository)}/tags`,
    ),
  putTag: (repository: string, source: string, target: string, overwrite = false) =>
    request<Image>(`/api/repositories/${repositoryPath(repository)}/tags`, {
      method: overwrite ? "PUT" : "POST",
      body: JSON.stringify({ source, target }),
    }),
  deleteTag: (repository: string, tag: string) =>
    request<void>(
      `/api/repositories/${repositoryPath(repository)}/tags/${encodeURIComponent(tag)}`,
      { method: "DELETE" },
    ),
  deleteManifest: (repository: string, digest: string) =>
    request<void>(
      `/api/repositories/${repositoryPath(repository)}/manifests/${encodeURIComponent(digest)}`,
      { method: "DELETE" },
    ),
  previewImageGC: (unusedForDays: number) =>
    request<ImageGCPreview>("/api/image-gc/preview", {
      method: "POST",
      body: JSON.stringify({ unusedForDays }),
    }),
  executeImageGC: (planId: string) =>
    request<ImageGCExecution>("/api/image-gc/execute", {
      method: "POST",
      body: JSON.stringify({ planId, confirmation: "DELETE" }),
    }),
}
