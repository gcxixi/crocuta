import React from "react"
import { Empty } from "antd"
import { Bar, CartesianGrid, ComposedChart, Legend, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import type { SeriesPoint } from "../api"

export function SeriesChart({ data }: { data: SeriesPoint[] }) {
  if (data.length === 0) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前时间窗没有事件" />
  const points = data.map((point) => ({ ...point, label: new Date(point.bucket).toLocaleString([], { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }) }))
  return <div style={{ width: "100%", height: 280 }}>
    <ResponsiveContainer>
      <ComposedChart data={points} margin={{ top: 8, right: 8, bottom: 8, left: 0 }}>
        <CartesianGrid strokeDasharray="3 3" vertical={false} />
        <XAxis dataKey="label" minTickGap={24} tick={{ fontSize: 11 }} />
        <YAxis yAxisId="events" allowDecimals={false} tick={{ fontSize: 11 }} />
        <YAxis yAxisId="users" orientation="right" allowDecimals={false} tick={{ fontSize: 11 }} />
        <Tooltip />
        <Legend />
        <Bar yAxisId="events" dataKey="count" name="事件数" fill="#6366f1" radius={[3, 3, 0, 0]} />
        <Line yAxisId="users" dataKey="users" name="影响用户" stroke="#10b981" strokeWidth={2} dot={data.length < 48} />
      </ComposedChart>
    </ResponsiveContainer>
  </div>
}
