import React from "react"
import ReactDOM from "react-dom/client"
import { App as AntApp, ConfigProvider, theme, type ThemeConfig } from "antd"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { BrowserRouter } from "react-router-dom"
import { AppShell } from "./components/AppShell"
import "./styles.css"

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

function Root() {
  const consoleTheme: ThemeConfig = {
    cssVar: { prefix: "sentryx" },
    algorithm: [theme.defaultAlgorithm, theme.compactAlgorithm],
    token: {
      colorPrimary: "#4f46e5",
      colorInfo: "#4f46e5",
      colorSuccess: "#16a34a",
      colorWarning: "#d97706",
      colorError: "#dc2626",
      colorBgLayout: "#f6f7f9",
      colorBgContainer: "#ffffff",
      colorBorder: "#dfe3e8",
      colorBorderSecondary: "#eceff3",
      colorText: "#17202a",
      colorTextSecondary: "#5f6b7a",
      borderRadius: 5,
      controlHeight: 30,
      controlHeightSM: 26,
      fontSize: 13,
      fontFamily: `Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`,
    },
    components: {
      Layout: {
        headerBg: "#ffffff",
        headerHeight: 46,
        headerPadding: "0 14px",
        lightSiderBg: "#ffffff",
        lightTriggerBg: "#ffffff",
        lightTriggerColor: "#5f6b7a",
        triggerHeight: 36,
      },
      Menu: {
        itemHeight: 34,
        itemMarginBlock: 2,
        itemMarginInline: 8,
        itemBorderRadius: 5,
        itemSelectedBg: "#eef2ff",
        itemSelectedColor: "#4338ca",
      },
      Table: {
        headerBg: "#f7f8fa",
        headerColor: "#52606d",
        rowHoverBg: "#f8faff",
        cellFontSizeSM: 12,
        cellPaddingBlockSM: 6,
        cellPaddingInlineSM: 8,
      },
      Card: {
        headerHeightSM: 34,
        bodyPaddingSM: 10,
        headerPaddingSM: 12,
      },
    },
  }

  return (
    <ConfigProvider componentSize="small" theme={consoleTheme}>
      <AntApp>
        <AppShell />
      </AntApp>
    </ConfigProvider>
  )
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
        <Root />
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>
)
