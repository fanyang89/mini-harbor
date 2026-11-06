import * as React from "react"
import * as AlertDialogPrimitive from "@radix-ui/react-alert-dialog"

import { buttonVariants } from "@/components/ui/button"
import { cn } from "@/lib/utils"

const AlertDialog = AlertDialogPrimitive.Root
const AlertDialogTrigger = AlertDialogPrimitive.Trigger

function AlertDialogContent({ className, ...props }: React.ComponentProps<typeof AlertDialogPrimitive.Content>) {
  return (
    <AlertDialogPrimitive.Portal>
      <AlertDialogPrimitive.Overlay
        className={cn(
          "fixed inset-0 z-50 bg-black/10 backdrop-blur-[2px] duration-200",
          "data-[state=open]:animate-[dialog-overlay-in_200ms_ease-out] data-[state=closed]:animate-[dialog-overlay-out_200ms_ease-in]",
          "motion-reduce:animate-none motion-reduce:transition-none",
        )}
      />
      <AlertDialogPrimitive.Content
        className={cn(
          "fixed inset-x-4 bottom-4 z-50 grid max-h-[calc(100dvh-2rem)] gap-4 overflow-y-auto rounded-xl bg-background p-4 ring-1 ring-border/60 outline-none duration-200",
          "sm:inset-x-auto sm:bottom-auto sm:left-1/2 sm:top-1/2 sm:w-[calc(100%-2rem)] sm:max-w-md sm:-translate-x-1/2 sm:-translate-y-1/2",
          "data-[state=open]:animate-[dialog-content-in_200ms_ease-out] data-[state=closed]:animate-[dialog-content-out_200ms_ease-in]",
          "motion-reduce:animate-none motion-reduce:transition-none",
          className,
        )}
        {...props}
      />
    </AlertDialogPrimitive.Portal>
  )
}

function AlertDialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("space-y-2 pr-8", className)} {...props} />
}

function AlertDialogFooter(props: React.ComponentProps<"div">) {
  return <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end" {...props} />
}

const AlertDialogTitle = AlertDialogPrimitive.Title
const AlertDialogDescription = AlertDialogPrimitive.Description

function AlertDialogAction({ className, ...props }: React.ComponentProps<typeof AlertDialogPrimitive.Action>) {
  return <AlertDialogPrimitive.Action className={cn(buttonVariants({ variant: "destructive" }), className)} {...props} />
}

function AlertDialogCancel({ className, ...props }: React.ComponentProps<typeof AlertDialogPrimitive.Cancel>) {
  return <AlertDialogPrimitive.Cancel className={cn(buttonVariants({ variant: "outline" }), className)} {...props} />
}

export { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger }
