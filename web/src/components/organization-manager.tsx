import { useState } from "react"
import { Building2, ChevronRight, Pencil, Plus, SearchX, Trash2 } from "lucide-react"

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
import { Button } from "@/components/ui/button"
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
import { api, type Organization } from "@/lib/api"

interface OrganizationManagerProps {
  canManage: boolean
  organizations: Organization[]
  totalCount: number
  loading: boolean
  searching: boolean
  onChanged: () => Promise<void>
  onSelect: (organization: Organization) => void
  onError: (message: string) => void
  onNotice: (message: string) => void
}

export function OrganizationManager({
  canManage,
  organizations,
  totalCount,
  loading,
  searching,
  onChanged,
  onSelect,
  onError,
  onNotice,
}: OrganizationManagerProps) {
  const { t, tn } = useI18n()
  const [editorOpen, setEditorOpen] = useState(false)
  const [editing, setEditing] = useState<Organization | null>(null)
  const [name, setName] = useState("")
  const [displayName, setDisplayName] = useState("")
  const [description, setDescription] = useState("")
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState<Organization | null>(null)
  const [deletePending, setDeletePending] = useState(false)

  const openCreate = () => {
    setEditing(null)
    setName("")
    setDisplayName("")
    setDescription("")
    setEditorOpen(true)
  }

  const openEdit = (organization: Organization) => {
    setEditing(organization)
    setName(organization.name)
    setDisplayName(organization.displayName)
    setDescription(organization.description)
    setEditorOpen(true)
  }

  const save = async () => {
    setSaving(true)
    onError("")
    try {
      if (editing) {
        await api.updateOrganization(editing.name, displayName.trim(), description.trim())
        onNotice(t("orgUpdated", { name: editing.name }))
      } else {
        await api.createOrganization(name.trim(), displayName.trim(), description.trim())
        onNotice(t("orgCreated", { name: name.trim() }))
      }
      setEditorOpen(false)
      await onChanged()
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("saveOrgError"))
    } finally {
      setSaving(false)
    }
  }

  const remove = async () => {
    if (!deleting) return
    setDeletePending(true)
    onError("")
    try {
      await api.deleteOrganization(deleting.name)
      onNotice(t("orgDeleted", { name: deleting.name }))
      setDeleting(null)
      await onChanged()
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("deleteOrgError"))
    } finally {
      setDeletePending(false)
    }
  }

  const validName = /^[a-z0-9]+(?:[._-][a-z0-9]+)*$/.test(name) && name.length <= 63
  const canSave =
    (editing !== null || validName) &&
    displayName.trim().length > 0 &&
    displayName.trim().length <= 100 &&
    description.trim().length <= 500

  return (
    <>
      <div className="flex min-w-0 items-center justify-between gap-3">
        <p className="min-w-0 flex-1 truncate text-xs text-muted-foreground">{t("orgsSubtitle")}</p>
        {canManage && (
          <Button size="sm" onClick={openCreate}>
            <Plus />
            {t("newOrg")}
          </Button>
        )}
      </div>

      {loading ? (
        <div aria-hidden className="space-y-1">
          {[0, 1, 2].map((item) => (
            <div key={item} className="flex items-center gap-3 px-3 py-3">
              <span className="pulse-soft size-4 shrink-0 rounded bg-muted" />
              <span className="pulse-soft h-4 w-32 rounded bg-muted" />
              <span className="pulse-soft h-4 w-56 rounded bg-muted" />
            </div>
          ))}
        </div>
      ) : organizations.length > 0 ? (
        <div className="rounded-lg border">
          {organizations.map((organization, index) => (
            <div
              key={organization.name}
              className={index > 0 ? "flex items-center gap-3 border-t px-3 py-2.5" : "flex items-center gap-3 px-3 py-2.5"}
            >
              <button
                type="button"
                onClick={() => onSelect(organization)}
                className="flex min-w-0 flex-1 items-center gap-3 rounded-md text-left transition-colors duration-150 hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/40 focus-visible:outline-none motion-reduce:transition-none"
              >
                <Building2 className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                <div className="min-w-0 flex-1">
                  <div className="flex min-w-0 items-baseline gap-2">
                    <span className="truncate text-sm font-medium">{organization.displayName}</span>
                    <code className="truncate font-mono text-xs text-muted-foreground">{organization.name}/</code>
                  </div>
                  <p className="truncate text-xs text-muted-foreground">{organization.description || t("noDescription")}</p>
                </div>
                <span className="shrink-0 text-xs text-muted-foreground">{tn("orgRepos", organization.repositoryCount)}</span>
                <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden />
              </button>
              {canManage && <div className="flex shrink-0 items-center gap-0.5">
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => openEdit(organization)}
                  aria-label={`${t("edit")} ${organization.displayName}`}
                  title={t("edit")}
                >
                  <Pencil />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-muted-foreground hover:text-destructive disabled:hover:text-muted-foreground"
                  onClick={() => setDeleting(organization)}
                  disabled={organization.repositoryCount > 0}
                  aria-label={`${t("delete")} ${organization.displayName}`}
                  title={organization.repositoryCount > 0 ? t("deleteOrgDisabled") : t("delete")}
                >
                  <Trash2 />
                </Button>
              </div>}
            </div>
          ))}
        </div>
      ) : totalCount > 0 ? (
        <div className="py-16 text-center">
          <div className="mb-3 flex justify-center text-muted-foreground">
            <SearchX className="size-8" aria-hidden />
          </div>
          <p className="text-sm font-medium">{t("noOrgsMatch")}</p>
        </div>
      ) : (
        <div className="py-16 text-center">
          <div className="mb-3 flex justify-center text-muted-foreground">
            <Building2 className="size-8" aria-hidden />
          </div>
          <p className="text-sm font-medium">{t("noOrgsTitle")}</p>
          <p className="mt-1 text-xs text-muted-foreground">{t("noOrgsHint")}</p>
          {canManage && (
            <Button size="sm" className="mt-4" onClick={openCreate}>
              <Plus />
              {t("newOrg")}
            </Button>
          )}
        </div>
      )}

      <Dialog open={editorOpen} onOpenChange={setEditorOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{editing ? t("editOrgTitle") : t("createOrgTitle")}</DialogTitle>
            <DialogDescription>{editing ? t("editOrgDesc") : t("createOrgDesc")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <label className="grid gap-2 text-sm font-medium">
              {t("name")}
              <Input
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder="platform-team"
                disabled={editing !== null}
                autoFocus={!editing}
              />
              {!editing && name && !validName && (
                <span className="text-xs font-normal text-destructive">{t("nameInvalid")}</span>
              )}
            </label>
            <label className="grid gap-2 text-sm font-medium">
              {t("displayName")}
              <Input
                value={displayName}
                onChange={(event) => setDisplayName(event.target.value)}
                placeholder="Platform Team"
                maxLength={100}
                autoFocus={editing !== null}
              />
            </label>
            <label className="grid gap-2 text-sm font-medium">
              {t("description")}
              <textarea
                value={description}
                onChange={(event) => setDescription(event.target.value)}
                placeholder={t("orgsSubtitle")}
                maxLength={500}
                rows={3}
                className="h-20 resize-none rounded-lg border border-input bg-transparent px-3 py-2 text-sm font-normal outline-none transition-shadow duration-150 placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/40 motion-reduce:transition-none"
              />
            </label>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditorOpen(false)}>
              {t("cancel")}
            </Button>
            <Button onClick={() => void save()} disabled={!canSave || saving}>
              {saving ? t("saving") : t("save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleting !== null} onOpenChange={(open) => !open && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("deleteOrgTitle", { name: deleting?.displayName ?? "" })}</AlertDialogTitle>
            <AlertDialogDescription>{t("deleteOrgDesc")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={(event) => {
                event.preventDefault()
                void remove()
              }}
              disabled={deletePending}
            >
              {deletePending ? t("deleting") : t("delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
