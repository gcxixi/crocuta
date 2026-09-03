import React from "react"
import { useNavigate, useParams, useSearchParams } from "react-router-dom"
import { IssueDetailView } from "../components/IssueDetailView"

export function IssueDetailPage() {
  const { projectId = "", issueId = "" } = useParams()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  return <IssueDetailView
    projectId={projectId}
    issueId={issueId}
    eventId={searchParams.get("event")}
    onEventChange={(eventId) => navigate(`/projects/${encodeURIComponent(projectId)}/issues/${encodeURIComponent(issueId)}?event=${encodeURIComponent(eventId)}`)}
  />
}
