package web

import (
	"net/http"
	"strconv"

	"github.com/servrai/pms-dummy/internal/store"
)

type roomsData struct {
	Rooms   []store.Room
	Edit    *store.Room
	FormNew bool
	Msg     string
	Statuses []string
}

func (s *Server) handleRooms(w http.ResponseWriter, r *http.Request) {
	rooms, err := s.Store.ListRooms()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := roomsData{Rooms: rooms, Msg: r.URL.Query().Get("msg"), Statuses: store.RoomStatuses}
	switch r.URL.Query().Get("form") {
	case "new":
		data.FormNew = true
	}
	if id, err := strconv.ParseInt(r.URL.Query().Get("edit"), 10, 64); err == nil && id > 0 {
		if rm, err := s.Store.RoomByID(id); err == nil && rm != nil {
			data.Edit = rm
		}
	}
	s.render(w, "rooms", data)
}

func (s *Server) handleRoomSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	number := r.PostFormValue("number")
	status := r.PostFormValue("status")
	category := r.PostFormValue("category")
	floor, _ := strconv.ParseInt(r.PostFormValue("floor"), 10, 64)
	if number == "" {
		seeOther(w, r, "/rooms", "room number is required")
		return
	}
	if !store.ValidRoomStatus(status) {
		seeOther(w, r, "/rooms", "invalid room status")
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if id > 0 {
		if err := s.Store.UpdateRoom(id, number, status, category, floor); err != nil {
			seeOther(w, r, "/rooms", "update failed: "+err.Error())
			return
		}
	} else {
		if _, err := s.Store.CreateRoom(number, status, category, floor); err != nil {
			seeOther(w, r, "/rooms", "create failed: "+err.Error())
			return
		}
	}
	s.maybeSyncAfterMutation()
	seeOther(w, r, "/rooms", "room saved")
}

func (s *Server) handleRoomDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if id > 0 {
		_ = s.Store.DeleteRoom(id)
	}
	s.maybeSyncAfterMutation()
	seeOther(w, r, "/rooms", "room deleted")
}
