import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"

import { en } from "./en"
import { zhCN } from "./zh-CN"

export type Locale = "en" | "zh-CN"
export type MessageKey = keyof typeof en
type StripOne<T> = T extends `${infer K}_one` ? K : never
type PluralBase = StripOne<MessageKey>

type Values = Record<string, string | number>

const STORAGE_KEY = "mini-harbor-locale"

const dictionaries: Record<Locale, Record<MessageKey, string>> = {
  en,
  "zh-CN": zhCN,
}

function detectLocale(): Locale {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === "en" || stored === "zh-CN") return stored
  } catch {
    // localStorage unavailable; fall back to language detection
  }
  return navigator.language.toLowerCase().startsWith("zh") ? "zh-CN" : "en"
}

function format(template: string, values?: Values) {
  if (!values) return template
  return template.replace(/\{(\w+)\}/g, (_, key: string) => String(values[key] ?? `{${key}}`))
}

interface I18nContextValue {
  locale: Locale
  setLocale: (locale: Locale) => void
  t: (key: MessageKey, values?: Values) => string
  tn: (key: PluralBase, count: number, values?: Values) => string
  formatDate: (value?: string) => string
}

const I18nContext = createContext<I18nContextValue | null>(null)

export function I18nProvider({ children }: { children: React.ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(detectLocale)

  useEffect(() => {
    document.documentElement.lang = locale
  }, [locale])

  const setLocale = useCallback((next: Locale) => {
    setLocaleState(next)
    try {
      localStorage.setItem(STORAGE_KEY, next)
    } catch {
      // persistence unavailable; keep the in-memory locale
    }
  }, [])

  const t = useCallback(
    (key: MessageKey, values?: Values) => format(dictionaries[locale][key] ?? key, values),
    [locale],
  )

  const tn = useCallback(
    (key: PluralBase, count: number, values?: Values) => {
      const dictionary = dictionaries[locale]
      const template = dictionary[`${key}_${count === 1 ? "one" : "other"}` as MessageKey] ?? key
      return format(template, { count, ...values })
    },
    [locale],
  )

  const formatDate = useCallback(
    (value?: string) => {
      if (!value) return dictionaries[locale].unknown
      const date = new Date(value)
      if (Number.isNaN(date.getTime())) return dictionaries[locale].unknown
      return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(date)
    },
    [locale],
  )

  const value = useMemo(() => ({ locale, setLocale, t, tn, formatDate }), [locale, setLocale, t, tn, formatDate])

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() {
  const context = useContext(I18nContext)
  if (!context) throw new Error("useI18n must be used within I18nProvider")
  return context
}
