import { useRef, useState } from "react"
import { CircleAlert } from "lucide-react"

import { formatBytes } from "@/components/tag-detail-dialog"
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
import { api, type ImageGCExecution, type ImageGCResult, type ImageGCPreview } from "@/lib/api"
import { cn } from "@/lib/utils"

interface ImageGCDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCompleted: () => Promise<void>
}

const resultClass = {
  deleted: "text-emerald-600",
  skipped: "text-amber-600",
  failed: "text-destructive",
} as const

export function ImageGCDialog({ open, onOpenChange, onCompleted }: ImageGCDialogProps) {
  const { t, formatDate } = useI18n()
  const [unusedForDays, setUnusedForDays] = useState("90")
  const [confirmation, setConfirmation] = useState("")
  const [preview, setPreview] = useState<ImageGCPreview | null>(null)
  const [execution, setExecution] = useState<ImageGCExecution | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const requestVersion = useRef(0)
  const days = Number(unusedForDays)
  const validDays = Number.isInteger(days) && days >= 1 && days <= 3650

  const reset = () => {
    requestVersion.current++
    setUnusedForDays("90")
    setConfirmation("")
    setPreview(null)
    setExecution(null)
    setError("")
    setLoading(false)
  }

  const changeOpen = (nextOpen: boolean) => {
    if (!nextOpen && loading) return
    if (!nextOpen) reset()
    onOpenChange(nextOpen)
  }

  const createPreview = async () => {
    if (!validDays) return
    const version = ++requestVersion.current
    setLoading(true)
    setError("")
    setExecution(null)
    setConfirmation("")
    try {
      const result = await api.previewImageGC(days)
      if (version === requestVersion.current) setPreview(result)
    } catch (cause) {
      if (version === requestVersion.current) {
        setPreview(null)
        setError(cause instanceof Error ? cause.message : t("gcPreviewError"))
      }
    } finally {
      if (version === requestVersion.current) setLoading(false)
    }
  }

  const execute = async () => {
    if (!preview || confirmation !== "DELETE") return
    const version = ++requestVersion.current
    setLoading(true)
    setError("")
    try {
      const result = await api.executeImageGC(preview.planId)
      if (version === requestVersion.current) setExecution(result)
      await onCompleted()
    } catch (cause) {
      if (version === requestVersion.current) {
        setError(cause instanceof Error ? cause.message : t("gcExecuteError"))
      }
    } finally {
      if (version === requestVersion.current) setLoading(false)
    }
  }

  const resultLabel = (result: ImageGCResult) => {
    if (result.status === "deleted") return t("gcDeleted")
    if (result.status === "skipped") return t("gcSkipped")
    return t("gcFailed")
  }

  const resultReason = (reason?: string) => {
    switch (reason) {
      case "digest no longer exists": return t("gcReasonMissing")
      case "tag set changed": return t("gcReasonTagsChanged")
      case "usage record is missing": return t("gcReasonUsageMissing")
      case "image usage is no longer stale": return t("gcReasonUsedRecently")
      case "registry revalidation failed": return t("gcReasonRegistryCheck")
      case "usage revalidation failed": return t("gcReasonUsageCheck")
      case "manifest deletion failed": return t("gcReasonDeleteFailed")
      default: return reason ?? ""
    }
  }

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("gcTitle")}</DialogTitle>
          <DialogDescription>{t("gcDesc")}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4" aria-busy={loading}>
          {error && (
            <p role="alert" className="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
              <CircleAlert className="size-4 shrink-0" aria-hidden />
              {error}
            </p>
          )}

          {!execution && (
            <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
              <label className="grid gap-2 text-sm font-medium">
                {t("gcUnusedForDays")}
                <Input
                  type="number"
                  min={1}
                  max={3650}
                  value={unusedForDays}
                  onChange={(event) => {
                    setUnusedForDays(event.target.value)
                    setPreview(null)
                    setConfirmation("")
                  }}
                  disabled={loading}
                />
              </label>
              <Button variant="outline" onClick={() => void createPreview()} disabled={!validDays || loading}>
                {loading && !preview ? t("gcPreviewing") : t("gcPreview")}
              </Button>
            </div>
          )}

          {!preview && !execution && (
            <p className="rounded-lg bg-muted/40 px-3 py-2 text-xs text-muted-foreground">{t("gcUnknownExcluded")}</p>
          )}

          {preview && !execution && (
            <>
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                <Metric label={t("gcManifests")} value={preview.manifestCount.toString()} />
                <Metric label={t("gcTags")} value={preview.tagCount.toString()} />
                <Metric label={t("gcLogicalSize")} value={formatBytes(preview.logicalSize)} />
                <Metric label={t("gcExpires")} value={formatDate(preview.expiresAt)} />
              </div>
              <div className="space-y-1 text-xs text-muted-foreground">
                <p>{t("gcCutoff", { date: formatDate(preview.cutoff) })}</p>
                <p>{t("gcLogicalSizeHint")}</p>
                <p>{t("gcUnknownExcluded")}</p>
              </div>

              {preview.candidates.length > 0 ? (
                <div className="max-h-64 overflow-auto rounded-lg border">
                  <table className="w-full min-w-144 border-collapse text-left text-sm">
                    <thead className="sticky top-0 border-b bg-background text-xs text-muted-foreground">
                      <tr>
                        <th className="px-3 py-2 font-medium">{t("gcRepository")}</th>
                        <th className="px-3 py-2 font-medium">{t("gcTags")}</th>
                        <th className="px-3 py-2 font-medium">{t("lastPulled")}</th>
                        <th className="px-3 py-2 text-right font-medium">{t("gcLogicalSize")}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {preview.candidates.map((candidate) => (
                        <tr key={`${candidate.repository}\0${candidate.digest}`} className="border-b last:border-b-0">
                          <td className="max-w-56 px-3 py-2">
                            <p className="break-all font-mono text-xs font-medium">{candidate.repository}</p>
                            <p className="break-all font-mono text-[10px] text-muted-foreground">{candidate.digest}</p>
                          </td>
                          <td className="max-w-48 px-3 py-2 font-mono text-xs break-words">{candidate.tags.join(", ") || t("gcUntagged")}</td>
                          <td className="px-3 py-2 text-xs whitespace-nowrap text-muted-foreground">{formatDate(candidate.lastUsed)}</td>
                          <td className="px-3 py-2 text-right font-mono text-xs whitespace-nowrap">{formatBytes(candidate.logicalSize)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <p className="rounded-lg border border-dashed px-3 py-6 text-center text-sm text-muted-foreground">{t("gcNoCandidates")}</p>
              )}

              {preview.candidates.length > 0 && (
                <label className="grid gap-2 text-sm font-medium">
                  {t("gcConfirmation")}
                  <Input
                    value={confirmation}
                    onChange={(event) => setConfirmation(event.target.value)}
                    placeholder="DELETE"
                    autoComplete="off"
                    disabled={loading}
                  />
                  <span className="text-xs font-normal text-muted-foreground">{t("gcConfirmationHint")}</span>
                </label>
              )}
            </>
          )}

          {execution && (
            <>
              <div className="grid grid-cols-3 gap-2" role="status" aria-live="polite">
                <Metric label={t("gcDeleted")} value={execution.deletedCount.toString()} valueClassName="text-emerald-600" />
                <Metric label={t("gcSkipped")} value={execution.skippedCount.toString()} valueClassName="text-amber-600" />
                <Metric label={t("gcFailed")} value={execution.failedCount.toString()} valueClassName="text-destructive" />
              </div>
              {execution.results.length > 0 && (
                <div className="max-h-64 overflow-auto rounded-lg border">
                  {execution.results.map((result) => (
                    <div key={`${result.repository}\0${result.digest}`} className="grid gap-1 border-b px-3 py-2 last:border-b-0 sm:grid-cols-[minmax(0,1fr)_auto]">
                      <div className="min-w-0">
                        <p className="break-all font-mono text-xs">{result.repository}@{result.digest}</p>
                        {result.reason && <p className="text-xs text-muted-foreground">{resultReason(result.reason)}</p>}
                      </div>
                      <span className={cn("text-xs font-medium", resultClass[result.status])}>{resultLabel(result)}</span>
                    </div>
                  ))}
                </div>
              )}
              <p className="rounded-lg bg-muted/40 px-3 py-2 text-xs text-muted-foreground">{t("gcDiskHint")}</p>
            </>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => changeOpen(false)} disabled={loading}>
            {execution ? t("close") : t("cancel")}
          </Button>
          {preview && !execution && preview.candidates.length > 0 && (
            <Button variant="destructive" onClick={() => void execute()} disabled={confirmation !== "DELETE" || loading}>
              {loading ? t("gcDeleting") : t("gcDelete")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Metric({ label, value, valueClassName }: { label: string; value: string; valueClassName?: string }) {
  return (
    <div className="rounded-lg border bg-muted/20 px-3 py-2">
      <p className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">{label}</p>
      <p className={cn("mt-1 break-words text-sm font-semibold", valueClassName)}>{value}</p>
    </div>
  )
}
