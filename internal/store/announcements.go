package store

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

type AnnouncementInput struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	Category  string `json:"category"`
	Published bool   `json:"published"`
	Pinned    bool   `json:"pinned"`
}

type Announcement struct {
	AnnouncementInput
	ID          string     `json:"id"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	PublishedAt *time.Time `json:"published_at"`
}

// Only the authenticated admin handler may include drafts; client filters never widen scope.
func (s *Store) Announcements(ctx context.Context, includeDrafts bool, page, size int) ([]Announcement, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM announcements WHERE published OR $1", includeDrafts).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,title,content,category,published,pinned,created_at,updated_at,published_at
	 FROM announcements WHERE published OR $1
	 ORDER BY pinned DESC,COALESCE(published_at,updated_at) DESC,id DESC LIMIT $2 OFFSET $3`, includeDrafts, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Announcement{}
	for rows.Next() {
		var a Announcement
		if err = rows.Scan(&a.ID, &a.Title, &a.Content, &a.Category, &a.Published, &a.Pinned, &a.CreatedAt, &a.UpdatedAt, &a.PublishedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, a)
	}
	return items, total, rows.Err()
}

func (s *Store) SaveAnnouncement(ctx context.Context, actor, id string, in AnnouncementInput) (string, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	if !utf8.ValidString(in.Title) || !utf8.ValidString(in.Content) || utf8.RuneCountInString(in.Title) < 1 || utf8.RuneCountInString(in.Title) > 120 || utf8.RuneCountInString(in.Content) < 1 || utf8.RuneCountInString(in.Content) > 10000 {
		return "", invalid("公告标题需为 1～120 字，正文需为 1～10000 字")
	}
	if in.Category != "notice" && in.Category != "update" && in.Category != "maintenance" {
		return "", invalid("公告分类无效")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return "", err
	}
	action := "announcement.update"
	if id == "" {
		id = ID()
		action = "announcement.create"
		_, err = tx.Exec(ctx, `INSERT INTO announcements(id,title,content,category,published,pinned,created_by,published_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,CASE WHEN $5 THEN now() ELSE NULL END)`, id, in.Title, in.Content, in.Category, in.Published, in.Pinned, actor)
	} else {
		var n int64
		tag, e := tx.Exec(ctx, `UPDATE announcements SET title=$2,content=$3,category=$4,pinned=$6,
		 published_at=CASE WHEN $5 AND NOT published THEN now() WHEN $5 THEN published_at ELSE NULL END,
		 published=$5,updated_at=now() WHERE id=$1`, id, in.Title, in.Content, in.Category, in.Published, in.Pinned)
		err = e
		n = tag.RowsAffected()
		if err == nil && n == 0 {
			return "", ErrNotFound
		}
	}
	if err != nil {
		return "", err
	}
	if err = audit(ctx, tx, actor, action, id, map[string]any{"title": in.Title, "published": in.Published, "pinned": in.Pinned}); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) DeleteAnnouncement(ctx context.Context, actor, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "DELETE FROM announcements WHERE id=$1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, actor, "announcement.delete", id, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
