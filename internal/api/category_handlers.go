package api

import (
	"encoding/json"
	"net/http"
)

type categoryDomain struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
	Position int `json:"position"`
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT jsonb_build_object('id',id,'name',name,'tags',tags,'position',position)
		FROM category_domains ORDER BY position,id`)
	if err != nil {
		s.databaseError(w, "list category domains", err)
		return
	}
	defer rows.Close()
	items := make([]categoryDomain, 0)
	for rows.Next() {
		var raw []byte
		var item categoryDomain
		if err := rows.Scan(&raw); err != nil {
			s.databaseError(w, "scan category domain", err)
			return
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			s.databaseError(w, "decode category domain", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.databaseError(w, "read category domains", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}
