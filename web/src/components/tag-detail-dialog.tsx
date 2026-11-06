import { useEffect, useState } from "react"
import { Check, Copy, Tag, Trash2 } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { useI18n } from "@/i18n"
import type { Image } from "@/lib/api"
import { cn } from "@/lib/utils"

export function formatBytes(bytes: number) {
  if (!bytes) return "0 B"
  const units = ["B", "KB", "MB", "GB", "TB"]
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** index).toFixed(index > 1 ? 1 : 0)} ${units[index]}`
}

export function shortDigest(digest: string) {
  return digest ? `${digest.slice(0, 19)}…${digest.slice(-8)}` : ""
}

export function friendlyMediaType(mediaType: string) {
  if (mediaType.includes("image.index") || mediaType.includes("manifest.list")) return "mediaIndex"
  if (mediaType.includes("oci.image")) return "mediaOci"
  if (mediaType.includes("docker")) return "mediaDocker"
  return "mediaManifest"
}

export function CopyButton({ value, label, className }: { value: string; label: string; className?: string }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!copied) return
    const timeout = window.setTimeout(() => setCopied(false), 1500)
    return () => window.clearTimeout(timeout)
  }, [copied])

  return (
    <button
      type="button"
      onClick={() => {
        void navigator.clipboard.writeText(value)
        setCopied(true)
      }}
      aria-label={label}
      className={cn(
        "inline-grid size-7 place-items-center rounded-md text-muted-foreground transition-colors duration-150 hover:bg-muted hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/40 focus-visible:outline-none motion-reduce:transition-none",
        className,
      )}
    >
      {copied ? <Check className="size-3.5 text-emerald-600" aria-hidden /> : <Copy className="size-3.5" aria-hidden />}
    </button>
  )
}

interface TagDetailDialogProps {
  canManage: boolean
  image: Image | null
  repository: string
  registry: string
  onClose: () => void
  onCopyTag: (image: Image) => void
  onOverwrite: (image: Image) => void
  onDelete: (image: Image) => void
}

export function TagDetailDialog({
  canManage,
  image,
  repository,
  registry,
  onClose,
  onCopyTag,
  onOverwrite,
  onDelete,
}: TagDetailDialogProps) {
  const { t, tn, formatDate } = useI18n()
  const open = image !== null

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="font-mono text-sm break-all">{repository}:{image?.tag}</DialogTitle>
          <DialogDescription className="sr-only">{t("pullCommand")}</DialogDescription>
        </DialogHeader>

        {image && (
          <div className="space-y-4">
            <div className="relative rounded-md border bg-muted/40 pr-1 pl-3">
              <div className="flex items-center justify-between gap-2 py-2 pr-1">
                <span className="text-xs font-medium text-muted-foreground">{t("pullCommand")}</span>
                <CopyButton value={`docker pull ${registry}/${repository}:${image.tag}`} label={t("copyPullCommand")} />
              </div>
              <code className="block pb-2 font-mono text-xs break-all">
                docker pull {registry}/{repository}:{image.tag}
              </code>
            </div>

            <dl className="space-y-2 text-sm">
              <Detail label={t("digest")} copy={image.digest} copyLabel={t("copyDigest")}>
                <code className="block font-mono text-xs break-all">{image.digest}</code>
              </Detail>
              <Detail label={t("mediaType")}>
                <span className="text-xs">{t(friendlyMediaType(image.mediaType))}</span>
              </Detail>
              <Detail label={t("size")}>
                <span className="text-xs">{formatBytes(image.size)}</span>
              </Detail>
              <Detail label={t("layers")}>
                <span className="text-xs">{image.layers}</span>
              </Detail>
              <Detail label={t("created")}>
                <span className="text-xs">{formatDate(image.created)}</span>
              </Detail>
              <Detail label={t("lastPulled")}>
                <span className="text-xs">{formatDate(image.lastUsed)}</span>
              </Detail>
              <Detail label={t("platform")}>
                <span className="text-xs">
                  {image.platforms?.length
                    ? tn("platformsCount", image.platforms.length)
                    : [image.os, image.architecture].filter(Boolean).join(" / ") || t("unknown")}
                </span>
              </Detail>
            </dl>

            {!!image.platforms?.length && (
              <div className="flex flex-wrap gap-1.5">
                {image.platforms.map((platform, index) => (
                  <Badge key={`${platform.os}-${platform.architecture}-${platform.variant}-${index}`} variant="secondary" className="font-mono">
                    {[platform.os, platform.architecture, platform.variant].filter(Boolean).join("/")}
                  </Badge>
                ))}
              </div>
            )}

            {canManage && <div className="grid gap-2 border-t pt-4 sm:grid-cols-3">
              <Button variant="outline" onClick={() => onCopyTag(image)}>
                <Tag />{t("copyTag")}
              </Button>
              <Button variant="outline" onClick={() => onOverwrite(image)}>
                <Copy />{t("overwrite")}
              </Button>
              <Button variant="destructive" onClick={() => onDelete(image)}>
                <Trash2 />{t("delete")}
              </Button>
            </div>}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

function Detail({
  label,
  copy,
  copyLabel,
  children,
}: {
  label: string
  copy?: string
  copyLabel?: string
  children: React.ReactNode
}) {
  return (
    <div className="flex items-start justify-between gap-3">
      <dt className="shrink-0 text-xs text-muted-foreground">{label}</dt>
      <dd className="flex min-w-0 flex-1 items-start justify-end gap-1 text-right">
        <span className="min-w-0">{children}</span>
        {copy && <CopyButton value={copy} label={copyLabel ?? label} className="-mt-0.5 shrink-0" />}
      </dd>
    </div>
  )
}
