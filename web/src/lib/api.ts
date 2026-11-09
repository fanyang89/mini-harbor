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

export interface RepositoryImage extends Image { repository: string }
export interface ImageConnection {
  nodes: RepositoryImage[]
  totalCount: number
  pageInfo: { endCursor: string; hasNextPage: boolean }
}

const imageFields = `repository tag digest mediaType size layers created lastUsed architecture os
  platforms { architecture os variant }`
const organizationFields = "name displayName description createdAt updatedAt repositoryCount"

async function graphql<T>(query: string, variables: Record<string, unknown>, mutation = false, signal?: AbortSignal, fresh = false): Promise<T> {
  const payload = { query, variables }
  const result = await request<{ data?: T; errors?: { message: string; extensions?: { status?: number } }[] }>(
    mutation ? "/graphql" : `/graphql?${new URLSearchParams({ query, variables: JSON.stringify(variables) })}`,
    mutation ? { method: "POST", body: JSON.stringify(payload), signal } : { signal, headers: fresh ? { "Cache-Control": "no-cache" } : undefined },
  )
  if (result.errors?.length) {
    throw new ApiRequestError(result.errors.map((error) => error.message).join("; "), result.errors[0].extensions?.status ?? 500)
  }
  if (!result.data) throw new ApiRequestError("GraphQL response is missing data", 502)
  return result.data
}

export const api = {
  auth: (signal?: AbortSignal) => request<AuthStatus>("/api/auth/me", { signal }),
  logout: () => request<void>("/auth/logout", { method: "POST" }),
  organizations: (signal?: AbortSignal) =>
    graphql<{ status: RegistryStatus; organizations: Organization[] }>(
      `query Organizations { status { online registry apiVersion } organizations { ${organizationFields} } }`, {}, false, signal),
  createOrganization: async (name: string, displayName: string, description: string) =>
    (await graphql<{ createOrganization: Organization }>(
      `mutation CreateOrganization($input: OrganizationInput!) { createOrganization(input: $input) { ${organizationFields} } }`,
      { input: { name, displayName, description } }, true)).createOrganization,
  updateOrganization: async (name: string, displayName: string, description: string) =>
    (await graphql<{ updateOrganization: Organization }>(
      `mutation UpdateOrganization($input: OrganizationInput!) { updateOrganization(input: $input) { ${organizationFields} } }`,
      { input: { name, displayName, description } }, true)).updateOrganization,
  deleteOrganization: (name: string) =>
    graphql(`mutation DeleteOrganization($name: String!) { deleteOrganization(name: $name) }`, { name }, true),
  catalog: (search: string, sort: "lastUsed" | "created" | "size", direction: "asc" | "desc", after = "", signal?: AbortSignal, fresh = false) =>
    graphql<{ status: RegistryStatus; images: ImageConnection }>(
      `query Catalog($filter: ImageFilter!, $sort: ImageSort!, $direction: SortDirection!, $after: String!) {
        status { online registry apiVersion }
        images(first: 50, after: $after, filter: $filter, sort: $sort, direction: $direction) {
          nodes { ${imageFields} } totalCount pageInfo { endCursor hasNextPage }
        }
      }`, { filter: { search }, sort: { lastUsed: "LAST_USED", created: "CREATED", size: "SIZE" }[sort], direction: direction.toUpperCase(), after }, false, signal, fresh),
  putTag: async (repository: string, source: string, target: string, overwrite = false) =>
    (await graphql<{ putTag: Image }>(
      `mutation PutTag($repository: String!, $source: String!, $target: String!, $overwrite: Boolean!) {
        putTag(repository: $repository, source: $source, target: $target, overwrite: $overwrite) { ${imageFields} }
      }`, { repository, source, target, overwrite }, true)).putTag,
  deleteTag: (repository: string, tag: string) =>
    graphql(`mutation DeleteTag($repository: String!, $tag: String!) { deleteTag(repository: $repository, tag: $tag) }`, { repository, tag }, true),
  deleteManifest: (repository: string, digest: string) =>
    graphql(`mutation DeleteManifest($repository: String!, $digest: String!) { deleteManifest(repository: $repository, digest: $digest) }`, { repository, digest }, true),
  previewImageGC: async (unusedForDays: number) =>
    (await graphql<{ previewImageGC: ImageGCPreview }>(
      `mutation PreviewImageGC($unusedForDays: Int!) { previewImageGC(unusedForDays: $unusedForDays) {
        planId generatedAt expiresAt cutoff manifestCount tagCount logicalSize
        candidates { repository digest tags lastUsed logicalSize }
      } }`, { unusedForDays }, true)).previewImageGC,
  executeImageGC: async (planId: string) =>
    (await graphql<{ executeImageGC: ImageGCExecution }>(
      `mutation ExecuteImageGC($planId: String!, $confirmation: String!) { executeImageGC(planId: $planId, confirmation: $confirmation) {
        deletedCount skippedCount failedCount results { repository digest status reason }
      } }`, { planId, confirmation: "DELETE" }, true)).executeImageGC,
}
