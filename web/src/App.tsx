import { startTransition, useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from "react"
import { Link, useMatch, useNavigate, useSearchParams } from "react-router"
import {
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  Box,
  Building2,
  Check,
  CircleAlert,
  Copy,
  Eraser,
  Languages,
  Layers3,
  LogIn,
  LogOut,
  RefreshCw,
  Search,
  SearchX,
  Tag,
  Trash2,
  X,
} from "lucide-react"

import { OrganizationManager } from "@/components/organization-manager"
import { ImageGCDialog } from "@/components/image-gc-dialog"
import { TagDetailDialog, CopyButton, formatBytes } from "@/components/tag-detail-dialog"
import { Button } from "@/components/ui/button"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { useI18n } from "@/i18n"
import { api, type AuthStatus, type Image, type Organization, type RegistryStatus } from "@/lib/api"
import { cn } from "@/lib/utils"

type DeleteMode = "tag" | "manifest"
type View = "images" | "organizations"
type ImageSort = "lastUsed" | "created" | "size"
type SortDirection = "asc" | "desc"

interface DeleteTarget {
  repository: string
  image: Image
  mode: DeleteMode
}

interface RepositoryImage extends Image {
  repository: string
}

const imageKey = (image: RepositoryImage) => `${image.repository}\0${image.tag}`
const timestamp = (value?: string) => {
  if (!value) return null
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? null : parsed
}
const imageSortValue = (image: RepositoryImage, sort: ImageSort) => {
  if (sort === "size") return image.size
  return timestamp(image[sort])
}

function App() {
  const { locale, setLocale, t, tn, formatDate } = useI18n()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const view: View = useMatch("/organizations") ? "organizations" : "images"
  const [identity, setIdentity] = useState<AuthStatus | null>(null)
  const [status, setStatus] = useState<RegistryStatus>({ online: false, registry: "localhost:5000" })
  const [organizations, setOrganizations] = useState<Organization[]>([])
  const [images, setImages] = useState<RepositoryImage[]>([])
  const [selectedImage, setSelectedImage] = useState<RepositoryImage | null>(null)
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(new Set())
  const search = searchParams.get("q") ?? ""
  const [imageSort, setImageSort] = useState<ImageSort>("lastUsed")
  const [sortDirection, setSortDirection] = useState<SortDirection>("desc")
  const [loadingImages, setLoadingImages] = useState(true)
  const [loadingOrganizations, setLoadingOrganizations] = useState(true)
  const [imageProgress, setImageProgress] = useState({ loaded: 0, total: 0 })
  const imageRequest = useRef<AbortController | null>(null)
  const organizationRequest = useRef<AbortController | null>(null)
  const activeView = useRef(view)
  activeView.current = view
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [tagDialogOpen, setTagDialogOpen] = useState(false)
  const [tagRepository, setTagRepository] = useState("")
  const [tagSource, setTagSource] = useState("")
  const [tagTarget, setTagTarget] = useState("")
  const [tagOverwrite, setTagOverwrite] = useState(false)
  const [savingTag, setSavingTag] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget | null>(null)
  const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false)
  const [imageGCOpen, setImageGCOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [tableScrollMetrics, setTableScrollMetrics] = useState({ contentWidth: 0, viewportWidth: 0 })
  const deferredSearch = useDeferredValue(search.trim().toLowerCase())
  const searchRef = useRef<HTMLInputElement>(null)
  const selectAllRef = useRef<HTMLInputElement>(null)
  const tableRef = useRef<HTMLTableElement>(null)
  const tableScrollRef = useRef<HTMLDivElement>(null)
  const tableTopScrollRef = useRef<HTMLDivElement>(null)
  const isMac = useMemo(() => /mac/i.test(navigator.platform), [])
  const routeSearch = searchParams.toString()
  const imageRoute = routeSearch ? `/?${routeSearch}` : "/"
  const organizationsRoute = routeSearch ? `/organizations?${routeSearch}` : "/organizations"
  const canManage = identity?.authenticated ?? false

  const setSearch = (value: string) => {
    const nextSearchParams = new URLSearchParams(searchParams)
    if (value) {
      nextSearchParams.set("q", value)
    } else {
      nextSearchParams.delete("q")
    }
    setSearchParams(nextSearchParams, { replace: true, preventScrollReset: true })
  }

  const loadImages = useCallback(async () => {
    if (activeView.current !== "images") return
    imageRequest.current?.abort()
    const controller = new AbortController()
    imageRequest.current = controller
    const { signal } = controller
    setLoadingImages(true)
    setError("")
    setImages([])
    setSelectedKeys(new Set())
    setImageProgress({ loaded: 0, total: 0 })
    try {
      const [nextStatus, payload] = await Promise.all([api.status(signal), api.repositories(signal)])
      if (signal.aborted) return
      setStatus(nextStatus)
      const repositories = [...new Set(payload.repositories)]
      setImageProgress({ loaded: 0, total: repositories.length })
      for (let offset = 0; offset < repositories.length; offset += 8) {
        const batch = await api.imageBatch(repositories.slice(offset, offset + 8), signal)
        if (signal.aborted) return
        const next = batch.repositories.flatMap(({ name, images }) =>
          images.map((image) => ({ ...image, repository: name })),
        )
        setImages((current) => [...current, ...next])
        setImageProgress({ loaded: Math.min(offset + 8, repositories.length), total: repositories.length })
      }
    } catch (cause) {
      if (signal.aborted) return
      setError(cause instanceof Error ? cause.message : t("registryError"))
    } finally {
      if (!signal.aborted) setLoadingImages(false)
    }
  }, [t])

  const loadOrganizations = useCallback(async () => {
    if (activeView.current !== "organizations") return
    organizationRequest.current?.abort()
    const controller = new AbortController()
    organizationRequest.current = controller
    const { signal } = controller
    setLoadingOrganizations(true)
    setError("")
    try {
      const [nextStatus, payload] = await Promise.all([api.status(signal), api.organizations(signal)])
      if (signal.aborted) return
      setStatus(nextStatus)
      startTransition(() => setOrganizations(payload.organizations))
    } catch (cause) {
      if (signal.aborted) return
      setError(cause instanceof Error ? cause.message : t("orgsError"))
    } finally {
      if (!signal.aborted) setLoadingOrganizations(false)
    }
  }, [t])

  useEffect(() => {
    const controller = new AbortController()
    const initialize = async () => {
      try {
        const nextIdentity = await api.auth(controller.signal)
        if (controller.signal.aborted) return
        setIdentity(nextIdentity)
      } catch (cause) {
        if (controller.signal.aborted) return
        setError(cause instanceof Error ? cause.message : t("sessionError"))
        setLoadingImages(false)
        setLoadingOrganizations(false)
      }
    }
    void initialize()
    return () => controller.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!identity) return
    if (view === "images") void loadImages()
    else void loadOrganizations()
    return () => {
      imageRequest.current?.abort()
      organizationRequest.current?.abort()
    }
  }, [identity, view, loadImages, loadOrganizations])

  useEffect(() => {
    if (!notice) return
    const timeout = window.setTimeout(() => setNotice(""), 3500)
    return () => window.clearTimeout(timeout)
  }, [notice])

  useEffect(() => {
    if (view !== "organizations") return
    setSelectedImage(null)
    setSelectedKeys(new Set())
  }, [view])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault()
        searchRef.current?.focus()
        searchRef.current?.select()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  const visibleImages = images
    .filter((image) => deferredSearch.endsWith("/")
      ? image.repository.toLowerCase().startsWith(deferredSearch)
      : `${image.repository} ${image.tag} ${image.digest} ${image.architecture} ${image.os}`
          .toLowerCase()
          .includes(deferredSearch))
    .sort((left, right) => {
      const leftValue = imageSortValue(left, imageSort)
      const rightValue = imageSortValue(right, imageSort)
      if (leftValue === null && rightValue === null) return imageKey(left).localeCompare(imageKey(right))
      if (leftValue === null) return 1
      if (rightValue === null) return -1
      const difference = (leftValue - rightValue) * (sortDirection === "asc" ? 1 : -1)
      return difference || imageKey(left).localeCompare(imageKey(right))
    })
  const organizationSearch = deferredSearch.endsWith("/") ? deferredSearch.slice(0, -1) : deferredSearch
  const visibleOrganizations = organizations.filter((organization) =>
    `${organization.name} ${organization.displayName} ${organization.description}`.toLowerCase().includes(organizationSearch),
  )
  const selectedImages = images.filter((image) => selectedKeys.has(imageKey(image)))
  const selectedVisibleCount = visibleImages.filter((image) => selectedKeys.has(imageKey(image))).length
  const allVisibleSelected = visibleImages.length > 0 && selectedVisibleCount === visibleImages.length

  const changeImageSort = (sort: ImageSort) => {
    if (imageSort === sort) {
      setSortDirection((current) => current === "asc" ? "desc" : "asc")
      return
    }
    setImageSort(sort)
    setSortDirection("desc")
  }

  const sortIcon = (sort: ImageSort) => {
    if (imageSort !== sort) return <ArrowUpDown className="size-3 opacity-40" aria-hidden />
    return sortDirection === "asc"
      ? <ArrowUp className="size-3" aria-hidden />
      : <ArrowDown className="size-3" aria-hidden />
  }

  const sortAriaValue = (sort: ImageSort) => {
    if (imageSort !== sort) return "none"
    return sortDirection === "asc" ? "ascending" : "descending"
  }

  useEffect(() => {
    if (selectAllRef.current) {
      selectAllRef.current.indeterminate = selectedVisibleCount > 0 && !allVisibleSelected
    }
  }, [allVisibleSelected, selectedVisibleCount])

  useEffect(() => {
    const table = tableRef.current
    const tableScroll = tableScrollRef.current
    const topScroll = tableTopScrollRef.current
    if (!table || !tableScroll || !topScroll) return

    const updateMetrics = () => {
      const next = { contentWidth: table.scrollWidth, viewportWidth: tableScroll.clientWidth }
      setTableScrollMetrics((current) =>
        current.contentWidth === next.contentWidth && current.viewportWidth === next.viewportWidth ? current : next,
      )
      topScroll.scrollLeft = tableScroll.scrollLeft
    }
    const syncTopScroll = () => {
      if (topScroll.scrollLeft !== tableScroll.scrollLeft) topScroll.scrollLeft = tableScroll.scrollLeft
    }
    const syncTableScroll = () => {
      if (tableScroll.scrollLeft !== topScroll.scrollLeft) tableScroll.scrollLeft = topScroll.scrollLeft
    }

    const resizeObserver = new ResizeObserver(updateMetrics)
    resizeObserver.observe(table)
    resizeObserver.observe(tableScroll)
    tableScroll.addEventListener("scroll", syncTopScroll, { passive: true })
    topScroll.addEventListener("scroll", syncTableScroll, { passive: true })
    updateMetrics()

    return () => {
      resizeObserver.disconnect()
      tableScroll.removeEventListener("scroll", syncTopScroll)
      topScroll.removeEventListener("scroll", syncTableScroll)
    }
  }, [loadingImages, view, visibleImages.length])

  const refresh = async () => {
    if (view === "images") {
      await loadImages()
    } else {
      await loadOrganizations()
    }
  }

  const openTagDialog = (repository: string, image: Image, overwrite = false) => {
    setTagRepository(repository)
    setTagSource(image.tag)
    setTagTarget(overwrite ? image.tag : "")
    setTagOverwrite(overwrite)
    setTagDialogOpen(true)
  }

  const openDeleteDialog = (repository: string, image: Image) => {
    setDeleteTarget({ repository, image, mode: "tag" })
  }

  const setDeleteMode = (mode: DeleteMode) => {
    setDeleteTarget((current) => current ? { ...current, mode } : current)
  }

  const saveTag = async () => {
    if (!tagRepository || !tagSource || !tagTarget) return
    setSavingTag(true)
    setError("")
    try {
      await api.putTag(tagRepository, tagSource, tagTarget, tagOverwrite)
      setTagDialogOpen(false)
      setNotice(t("tagSaved", { repo: tagRepository, tag: tagTarget }))
      await loadImages()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("saveTagError"))
    } finally {
      setSavingTag(false)
    }
  }

  const deleteImage = async () => {
    if (!deleteTarget) return
    setDeleting(true)
    setError("")
    try {
      if (deleteTarget.mode === "tag") {
        await api.deleteTag(deleteTarget.repository, deleteTarget.image.tag)
        setNotice(t("tagDeleted", { repo: deleteTarget.repository, tag: deleteTarget.image.tag }))
      } else {
        await api.deleteManifest(deleteTarget.repository, deleteTarget.image.digest)
        setNotice(t("manifestDeleted"))
      }
      setDeleteTarget(null)
      await loadImages()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("deleteError"))
    } finally {
      setDeleting(false)
    }
  }

  const deleteSelectedTags = async () => {
    if (selectedImages.length === 0) return
    setDeleting(true)
    setError("")
    const results = await Promise.allSettled(
      selectedImages.map((image) => api.deleteTag(image.repository, image.tag)),
    )
    const failedImages = selectedImages.filter((_, index) => results[index].status === "rejected")
    setBulkDeleteOpen(false)
    await loadImages()
    if (failedImages.length > 0) {
      setSelectedKeys(new Set(failedImages.map(imageKey)))
      setError(t("bulkDeletePartial", { failed: failedImages.length, count: selectedImages.length }))
    } else {
      setNotice(tn("tagsDeleted", selectedImages.length))
    }
    setDeleting(false)
  }

  const logout = async () => {
    try {
      await api.logout()
      window.location.assign("/")
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("logoutError"))
    }
  }

  const login = () => {
    const returnPath = `${window.location.pathname}${window.location.search}`
    window.location.assign(`/auth/login?return=${encodeURIComponent(returnPath)}`)
  }

  const toggleLocale = () => setLocale(locale === "en" ? "zh-CN" : "en")
  const busy = view === "images" ? loadingImages : loadingOrganizations
  const searchPlaceholder = view === "organizations" ? t("searchOrgs") : t("searchImages")

  return (
    <div className="mx-auto flex min-h-svh w-full max-w-7xl flex-col px-4 py-8">
      <header className="mb-6 flex items-center justify-between gap-2">
        <h1 className="text-lg font-semibold tracking-tight">Mini Harbor</h1>
        <div className="flex items-center gap-1.5">
          <Button variant="ghost" size="icon-sm" onClick={toggleLocale} aria-label={t("switchLanguage")} title={t("switchLanguage")}>
            <Languages />
          </Button>
          <div className="flex items-center gap-0.5 rounded-lg border border-input p-0.5">
            <Link
              to={imageRoute}
              replace={view === "images"}
              aria-current={view === "images" ? "page" : undefined}
              className={cn(
                "flex h-6 items-center gap-1.5 rounded-md px-2 text-xs font-medium transition-colors duration-150 focus-visible:ring-3 focus-visible:ring-ring/40 focus-visible:outline-none motion-reduce:transition-none",
                view === "images" ? "bg-muted" : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Box className="size-3.5" aria-hidden />
              {t("images")}
            </Link>
            <Link
              to={organizationsRoute}
              replace={view === "organizations"}
              aria-current={view === "organizations" ? "page" : undefined}
              className={cn(
                "flex h-6 items-center gap-1.5 rounded-md px-2 text-xs font-medium transition-colors duration-150 focus-visible:ring-3 focus-visible:ring-ring/40 focus-visible:outline-none motion-reduce:transition-none",
                view === "organizations" ? "bg-muted" : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Building2 className="size-3.5" aria-hidden />
              {t("organizations")}
            </Link>
          </div>
          <Button variant="ghost" size="icon-sm" onClick={() => void refresh()} aria-label={t("refresh")} title={t("refresh")}>
            <RefreshCw className={cn(busy && "animate-spin motion-reduce:animate-none")} />
          </Button>
          {identity?.enabled && canManage && (
            <Button variant="ghost" size="icon-sm" onClick={() => void logout()} aria-label={t("signOut")} title={identity.user?.name ?? t("signOut")}>
              <LogOut />
            </Button>
          )}
          {identity?.enabled && !canManage && (
            <Button variant="ghost" size="icon-sm" onClick={login} aria-label={t("signIn")} title={t("signIn")}>
              <LogIn />
            </Button>
          )}
        </div>
      </header>

      <div className="relative mb-4">
        <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden />
        <input
          ref={searchRef}
          type="search"
          value={search}
          autoFocus
          onChange={(event) => setSearch(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape" && search) {
              event.preventDefault()
              setSearch("")
            }
          }}
          placeholder={searchPlaceholder}
          aria-label={searchPlaceholder}
          className="h-12 w-full rounded-lg border border-input bg-transparent pr-16 pl-9 text-base outline-none transition-shadow duration-150 placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/40 motion-reduce:transition-none [&::-webkit-search-cancel-button]:hidden"
        />
        <span className="absolute top-1/2 right-2.5 flex -translate-y-1/2 items-center gap-1.5">
          {!search && (
            <kbd aria-hidden className="hidden rounded border px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground sm:block">
              {isMac ? "⌘ K" : "Ctrl K"}
            </kbd>
          )}
          {search && (
            <button
              type="button"
              onClick={() => {
                setSearch("")
                searchRef.current?.focus()
              }}
              aria-label={t("clearSearch")}
              className="grid size-6 place-items-center rounded-md text-muted-foreground transition-colors duration-150 hover:bg-muted hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/40 focus-visible:outline-none motion-reduce:transition-none"
            >
              <X className="size-3.5" aria-hidden />
            </button>
          )}
        </span>
      </div>

      <div className="mb-4 min-h-5">
        {error ? (
          <div role="alert" className="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-1.5 text-sm text-destructive">
            <CircleAlert className="size-4 shrink-0" aria-hidden />
            <span className="min-w-0 flex-1 truncate">{error}</span>
            <Button
              variant="outline"
              size="sm"
              className="h-6 border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive"
              onClick={() => void refresh()}
            >
              {t("retry")}
            </Button>
            <button
              type="button"
              onClick={() => setError("")}
              aria-label={t("dismiss")}
              className="grid size-6 shrink-0 place-items-center rounded-md transition-colors duration-150 hover:bg-destructive/10 focus-visible:ring-3 focus-visible:ring-ring/40 focus-visible:outline-none motion-reduce:transition-none"
            >
              <X className="size-3.5" aria-hidden />
            </button>
          </div>
        ) : notice ? (
          <p className="flex items-center gap-2 px-1 text-xs text-emerald-600">
            <Check className="size-3.5 shrink-0" aria-hidden />
            {notice}
          </p>
        ) : null}
      </div>

      {view === "images" && imageProgress.total > imageProgress.loaded && (
        <p role="status" className="mb-3 text-xs text-muted-foreground">
          {t("imageLoadProgress", imageProgress)}
        </p>
      )}

      <main className="flex flex-1 flex-col gap-4">
        {view === "organizations" ? (
          <OrganizationManager
            canManage={canManage}
            organizations={visibleOrganizations}
            totalCount={organizations.length}
            loading={loadingOrganizations}
            searching={!!deferredSearch}
            onChanged={loadOrganizations}
            onSelect={(organization) => {
              const nextSearchParams = new URLSearchParams(searchParams)
              nextSearchParams.set("q", `${organization.name}/`)
              setSelectedImage(null)
              setSelectedKeys(new Set())
              navigate({ pathname: "/", search: `?${nextSearchParams.toString()}` })
            }}
            onError={setError}
            onNotice={setNotice}
          />
        ) : loadingImages && images.length === 0 ? (
          <SkeletonRows detailed />
        ) : images.length > 0 ? (
          <section aria-label={t("images")} className="overflow-clip rounded-lg border">
            <div className="flex min-h-10 flex-wrap items-center gap-3 border-b bg-muted/20 px-3 py-1.5">
              <span className="min-w-0 flex-1 text-xs text-muted-foreground">
                {selectedKeys.size > 0 ? tn("selectedCount", selectedKeys.size) : tn("imagesCount", visibleImages.length)}
              </span>
              {canManage && <Button variant="ghost" size="sm" onClick={() => setImageGCOpen(true)}>
                <Eraser />
                {t("cleanupImages")}
              </Button>}
              {canManage && selectedKeys.size > 0 && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                  onClick={() => setBulkDeleteOpen(true)}
                >
                  <Trash2 />
                  {t("deleteSelected")}
                </Button>
              )}
            </div>
            {visibleImages.length > 0 ? (
              <>
                <div
                  ref={tableTopScrollRef}
                  aria-hidden
                  className={cn(
                    "sticky top-0 z-20 h-4 overflow-x-auto overflow-y-hidden border-b bg-background",
                    tableScrollMetrics.contentWidth <= tableScrollMetrics.viewportWidth && "hidden",
                  )}
                >
                  <div className="h-px" style={{ width: tableScrollMetrics.contentWidth }} />
                </div>
                <div ref={tableScrollRef} className="overflow-x-auto">
                  <table ref={tableRef} className="w-full min-w-184 table-fixed border-collapse text-left text-sm">
                  <thead className="border-b bg-muted/10 text-xs text-muted-foreground">
                    <tr>
                      {canManage && <th scope="col" className="w-10 px-3 py-2 font-medium">
                        <input
                          ref={selectAllRef}
                          type="checkbox"
                          checked={allVisibleSelected}
                          disabled={visibleImages.length === 0}
                          onChange={() => {
                            setSelectedKeys((current) => {
                              const next = new Set(current)
                              for (const image of visibleImages) {
                                if (allVisibleSelected) next.delete(imageKey(image))
                                else next.add(imageKey(image))
                              }
                              return next
                            })
                          }}
                          aria-label={t("selectAll")}
                          className="block size-4 accent-foreground"
                        />
                      </th>}
                      <th scope="col" className="px-3 py-2 font-medium">{t("imageReference")}</th>
                      <th scope="col" aria-sort={sortAriaValue("size")} className="w-20 font-medium">
                        <button
                          type="button"
                          onClick={() => changeImageSort("size")}
                          className="flex w-full items-center justify-end gap-1 rounded-sm px-3 py-2 hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/40 focus-visible:outline-none"
                        >
                          {t("size")}
                          {sortIcon("size")}
                        </button>
                      </th>
                      <th scope="col" aria-sort={sortAriaValue("lastUsed")} className="w-36 font-medium">
                        <button
                          type="button"
                          onClick={() => changeImageSort("lastUsed")}
                          className="flex w-full items-center gap-1 rounded-sm px-3 py-2 hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/40 focus-visible:outline-none"
                        >
                          {t("lastPulled")}
                          {sortIcon("lastUsed")}
                        </button>
                      </th>
                      <th scope="col" aria-sort={sortAriaValue("created")} className="w-36 font-medium">
                        <button
                          type="button"
                          onClick={() => changeImageSort("created")}
                          className="flex w-full items-center gap-1 rounded-sm px-3 py-2 hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/40 focus-visible:outline-none"
                        >
                          {t("created")}
                          {sortIcon("created")}
                        </button>
                      </th>
                      {canManage && <th scope="col" className="w-28 px-2 py-2"><span className="sr-only">{t("actions")}</span></th>}
                    </tr>
                  </thead>
                  <tbody>
                    {visibleImages.map((image) => {
                      const key = imageKey(image)
                      const selected = selectedKeys.has(key)
                      return (
                        <tr
                          key={key}
                          className={cn(
                            "border-b transition-colors duration-150 last:border-b-0 hover:bg-muted/30 motion-reduce:transition-none",
                            selected && "bg-muted/40",
                          )}
                        >
                          {canManage && <td className="px-3 py-2">
                            <input
                              type="checkbox"
                              checked={selected}
                              onChange={() => {
                                setSelectedKeys((current) => {
                                  const next = new Set(current)
                                  if (next.has(key)) next.delete(key)
                                  else next.add(key)
                                  return next
                                })
                              }}
                              aria-label={t("selectImage", { name: `${image.repository}:${image.tag}` })}
                              className="block size-4 accent-foreground"
                            />
                          </td>}
                          <td className="px-3 py-2">
                            <div className="flex w-full items-center gap-1">
                              <button
                                type="button"
                                onClick={() => setSelectedImage(image)}
                                className="flex min-w-0 flex-1 items-center gap-2 rounded-sm text-left font-mono font-medium hover:underline focus-visible:ring-3 focus-visible:ring-ring/40 focus-visible:outline-none"
                              >
                                <Tag className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                                <span className="truncate"><span className="text-muted-foreground">{image.repository}:</span>{image.tag}</span>
                              </button>
                              <CopyButton
                                value={`${image.repository}:${image.tag}`}
                                label={t("copyImageName")}
                                className="shrink-0"
                              />
                            </div>
                          </td>
                          <td className="px-3 py-2 text-right font-mono text-xs whitespace-nowrap">{formatBytes(image.size)}</td>
                          <td className="px-3 py-2 text-xs whitespace-nowrap text-muted-foreground">{formatDate(image.lastUsed)}</td>
                          <td className="px-3 py-2 text-xs whitespace-nowrap text-muted-foreground">{formatDate(image.created)}</td>
                          {canManage && <td className="px-2 py-2">
                            <div className="flex items-center justify-end gap-0.5">
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                onClick={() => openTagDialog(image.repository, image)}
                                aria-label={`${t("copyTag")} ${image.repository}:${image.tag}`}
                                title={t("copyTag")}
                              >
                                <Tag />
                              </Button>
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                onClick={() => openTagDialog(image.repository, image, true)}
                                aria-label={`${t("overwrite")} ${image.repository}:${image.tag}`}
                                title={t("overwrite")}
                              >
                                <Copy />
                              </Button>
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                                onClick={() => openDeleteDialog(image.repository, image)}
                                aria-label={`${t("delete")} ${image.repository}:${image.tag}`}
                                title={t("delete")}
                              >
                                <Trash2 />
                              </Button>
                            </div>
                          </td>}
                        </tr>
                      )
                    })}
                  </tbody>
                  </table>
                </div>
              </>
            ) : (
              <EmptyState icon={<SearchX className="size-8" aria-hidden />} title={t("noImagesMatch")} />
            )}
          </section>
        ) : (
          <EmptyState icon={<Layers3 className="size-8" aria-hidden />} title={t("registryEmptyTitle")} hint={canManage ? t("registryEmptyHint") : undefined}>
            {canManage && <PushCommands registry={status.registry} repository="hello-world" />}
          </EmptyState>
        )}
      </main>

      <footer className="mt-8 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
        <span
          className={cn("size-1.5 rounded-full", status.online ? "bg-emerald-500" : "bg-destructive")}
          aria-hidden
        />
        <span>{status.online ? t("online") : t("offline")}</span>
        <span aria-hidden>·</span>
        <code className="truncate font-mono">{status.registry}</code>
        {identity?.user?.name && (
          <>
            <span aria-hidden>·</span>
            <span className="truncate">{identity.user.name}</span>
          </>
        )}
      </footer>

      <TagDetailDialog
        canManage={canManage}
        image={selectedImage}
        repository={selectedImage?.repository ?? ""}
        registry={status.registry}
        onClose={() => setSelectedImage(null)}
        onCopyTag={(image) => {
          const repository = selectedImage?.repository
          if (!repository) return
          setSelectedImage(null)
          openTagDialog(repository, image)
        }}
        onOverwrite={(image) => {
          const repository = selectedImage?.repository
          if (!repository) return
          setSelectedImage(null)
          openTagDialog(repository, image, true)
        }}
        onDelete={(image) => {
          const repository = selectedImage?.repository
          if (!repository) return
          setSelectedImage(null)
          openDeleteDialog(repository, image)
        }}
      />

      <ImageGCDialog
        open={imageGCOpen}
        onOpenChange={setImageGCOpen}
        onCompleted={loadImages}
      />

      <Dialog open={tagDialogOpen} onOpenChange={setTagDialogOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{tagOverwrite ? t("overwriteTagTitle") : t("createTag")}</DialogTitle>
            <DialogDescription>{t("tagDialogDesc", { repo: tagRepository })}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <label className="grid gap-2 text-sm font-medium">
              {t("sourceTag")}
              <select
                value={tagSource}
                onChange={(event) => setTagSource(event.target.value)}
                className="h-8 rounded-lg border border-input bg-transparent px-2.5 text-sm font-normal outline-none transition-shadow duration-150 focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/40 motion-reduce:transition-none"
              >
                {images.filter((image) => image.repository === tagRepository).map((image) => (
                  <option key={image.tag} value={image.tag}>
                    {image.tag}
                  </option>
                ))}
              </select>
            </label>
            <label className="grid gap-2 text-sm font-medium">
              {t("targetTag")}
              <Input value={tagTarget} onChange={(event) => setTagTarget(event.target.value)} placeholder="release-2026.08" autoFocus />
            </label>
            {tagOverwrite && <p className="rounded-md bg-muted/40 px-3 py-2 text-xs text-muted-foreground">{t("overwriteHint")}</p>}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setTagDialogOpen(false)}>
              {t("cancel")}
            </Button>
            <Button onClick={() => void saveTag()} disabled={!tagSource || !tagTarget || savingTag}>
              {savingTag ? t("saving") : t("saveTag")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleteTarget !== null} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("deleteScopeTitle")}</AlertDialogTitle>
            <AlertDialogDescription>{t("deleteScopeDesc")}</AlertDialogDescription>
          </AlertDialogHeader>
          {deleteTarget && (
            <div className="grid gap-2">
              <label
                className={cn(
                  "flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors duration-150 motion-reduce:transition-none",
                  deleteTarget.mode === "tag" ? "border-destructive/40 bg-destructive/5" : "hover:bg-muted/40",
                )}
              >
                <input
                  type="radio"
                  name="delete-mode"
                  checked={deleteTarget.mode === "tag"}
                  onChange={() => setDeleteMode("tag")}
                  className="mt-0.5 size-4 accent-destructive"
                />
                <span>
                  <span className="block text-sm font-medium">{t("deleteTag")}</span>
                  <span className="mt-0.5 block text-xs text-muted-foreground">
                    {t("deleteTagDesc", { tag: deleteTarget.image.tag })}
                  </span>
                </span>
              </label>
              <label
                className={cn(
                  "flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors duration-150 motion-reduce:transition-none",
                  deleteTarget.mode === "manifest" ? "border-destructive/40 bg-destructive/5" : "hover:bg-muted/40",
                )}
              >
                <input
                  type="radio"
                  name="delete-mode"
                  checked={deleteTarget.mode === "manifest"}
                  onChange={() => setDeleteMode("manifest")}
                  className="mt-0.5 size-4 accent-destructive"
                />
                <span>
                  <span className="block text-sm font-medium text-destructive">{t("deleteImage")}</span>
                  <span className="mt-0.5 block text-xs text-muted-foreground">{t("deleteImageDesc")}</span>
                </span>
              </label>
            </div>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction onClick={(event) => { event.preventDefault(); void deleteImage() }} disabled={deleting}>
              {deleting ? <>{t("deleting")}</> : t("delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={bulkDeleteOpen} onOpenChange={setBulkDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{tn("deleteSelectedTitle", selectedImages.length)}</AlertDialogTitle>
            <AlertDialogDescription>{t("deleteSelectedDesc")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={(event) => {
                event.preventDefault()
                void deleteSelectedTags()
              }}
              disabled={deleting}
            >
              {deleting ? t("deleting") : t("delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function SkeletonRows({ detailed = false }: { detailed?: boolean }) {
  return (
    <div aria-hidden className="space-y-1">
      {[0, 1, 2, 3].map((item) => (
        <div key={item} className="flex items-center gap-3 px-3 py-2">
          <span className="pulse-soft size-4 shrink-0 rounded bg-muted" />
          <span className="pulse-soft h-4 w-40 rounded bg-muted" />
          {detailed && <span className="pulse-soft h-3 w-56 rounded bg-muted" />}
        </div>
      ))}
    </div>
  )
}

function EmptyState({
  icon,
  title,
  hint,
  children,
}: {
  icon: React.ReactNode
  title: string
  hint?: string
  children?: React.ReactNode
}) {
  return (
    <div className="py-16 text-center">
      <div className="mb-3 flex justify-center text-muted-foreground">{icon}</div>
      <p className="text-sm font-medium">{title}</p>
      {hint && <p className="mt-1 text-xs text-muted-foreground">{hint}</p>}
      {children}
    </div>
  )
}

function PushCommands({ registry, repository }: { registry: string; repository: string }) {
  const { t } = useI18n()
  const command = `docker tag hello-world ${registry}/${repository}:latest\ndocker push ${registry}/${repository}:latest`
  return (
    <div className="relative mx-auto mt-4 w-full max-w-lg rounded-md border bg-muted/40 text-left">
      <CopyButton value={command} label={t("copy")} className="absolute top-1.5 right-1.5" />
      <code className="block overflow-x-auto p-3 pr-10 font-mono text-xs whitespace-pre">
        {command}
      </code>
    </div>
  )
}

export default App
