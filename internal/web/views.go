package web

import (
	"context"
	"fmt"
	"time"

	"piping/internal/job"
	"piping/internal/queue"
	"piping/internal/session"
	"piping/internal/user"
)

type baseView struct {
	Username string
	IsStaff  bool
	Build    string
}

type jobView struct {
	SubmittedAt  time.Time
	TimeSince    string
	DocumentName string
	Pages        int
	Copies       int
	Cost         int
	State        string
	Destination  string
}

func (s *Server) base(ctx context.Context) baseView {
	sess := ctx.Value(sessionKey{}).(session.Session)
	return baseView{
		Username: sess.Sub,
		IsStaff:  user.RoleRank(sess.Role) >= user.RoleRank(user.RoleStaff),
		Build:    s.build,
	}
}

func toJobViews(js []job.WithDestinationName) []jobView {
	out := make([]jobView, 0, len(js))
	for _, j := range js {
		out = append(out, toJobView(j))
	}
	return out
}

func toJobView(j job.WithDestinationName) jobView {
	if j.DestinationName == "" {
		j.DestinationName = "None"
	}
	return jobView{
		SubmittedAt:  j.SubmittedAt,
		TimeSince:    formatTimeSince(j.SubmittedAt),
		DocumentName: j.DocumentName,
		Pages:        j.NumPages,
		Copies:       j.Copies,
		Cost:         j.Cost,
		State:        j.State.String(),
		Destination:  j.DestinationName,
	}
}

func formatTimeSince(t time.Time) string {
	d := time.Since(t)
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

type queueView struct {
	ID   int64
	Name string
}

func toQueueViews(qs []queue.Queue) []queueView {
	out := make([]queueView, 0, len(qs))
	for _, q := range qs {
		out = append(out, queueView{ID: q.ID, Name: q.Name})
	}
	return out
}

type homeView struct {
	baseView
	Remaining int
	Granted   int
	Queues    []queueView
	Recent    []jobView
	MaxCopies int
	MaxSize   int64
}

type jobsView struct {
	baseView
	Jobs []jobView
}

type resultView struct {
	baseView
	Title   string
	Message string
}

type adminView struct {
	baseView
}
