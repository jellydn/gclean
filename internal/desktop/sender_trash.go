package desktop

import (
	"fmt"
	"net/http"

	"gclean/internal/engine"
	"gclean/internal/storage"
)

const senderTrashConfirmation = "MOVE SENDER MAIL TO TRASH"

type senderTrashRequest struct {
	Sender       string `json:"sender"`
	PreviewID    string `json:"previewId,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
}

type senderTrashPreviewResponse struct {
	Sender    string `json:"sender"`
	Query     string `json:"query"`
	Count     int    `json:"count"`
	Bytes     int64  `json:"bytes"`
	PreviewID string `json:"previewId"`
}

func (a *App) registerTrashRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/trash", a.api(a.trash))
	mux.HandleFunc("POST /api/sender/preview", a.api(a.senderTrashPreview))
	mux.HandleFunc("POST /api/sender/trash", a.api(a.senderTrash))
}

func (a *App) senderTrashPreview(w http.ResponseWriter, r *http.Request) error {
	var request senderTrashRequest
	if err := decodeJSON(r, &request); err != nil {
		return err
	}
	sender, err := engine.NormalizeSenderAddress(request.Sender)
	if err != nil {
		return &statusError{http.StatusBadRequest, err.Error()}
	}
	if !a.operation.TryLock() {
		return &statusError{http.StatusConflict, "another operation is already running"}
	}
	defer a.operation.Unlock()
	client, account, err := a.getClientAndAccount()
	if err != nil {
		return err
	}
	preview, err := engine.PreviewSenderTrash(client, account, sender)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, senderTrashPreviewResponse{
		Sender: preview.Sender, Query: preview.Query, Count: preview.Count,
		Bytes: preview.Bytes, PreviewID: preview.ID,
	})
}

func (a *App) senderTrash(w http.ResponseWriter, r *http.Request) error {
	var request senderTrashRequest
	if err := decodeJSON(r, &request); err != nil {
		return err
	}
	if request.Confirmation != senderTrashConfirmation {
		return &statusError{http.StatusBadRequest, "type MOVE SENDER MAIL TO TRASH to confirm"}
	}
	sender, err := engine.NormalizeSenderAddress(request.Sender)
	if err != nil {
		return &statusError{http.StatusBadRequest, err.Error()}
	}
	if !a.operation.TryLock() {
		return &statusError{http.StatusConflict, "another operation is already running"}
	}
	defer a.operation.Unlock()
	lock, err := storage.AcquireMutationLock(a.cfg.CachePath)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	client, account, err := a.getClientAndAccount()
	if err != nil {
		return err
	}
	preview, err := engine.PreviewSenderTrash(client, account, sender)
	if err != nil {
		return err
	}
	if preview.Count == 0 {
		return &statusError{http.StatusBadRequest, "no messages from that exact sender were found"}
	}
	if request.PreviewID == "" || request.PreviewID != preview.ID {
		return &statusError{http.StatusConflict, "sender preview changed; preview again and review the count before continuing"}
	}
	journal := &engine.Reconciler{
		Store: a.store, CachePath: a.cfg.CachePath, Account: account,
		Client: client, MutationLockHeld: true,
	}
	outcome, err := engine.ApplySenderTrash(journal, preview)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, actionResponse{
		Message: fmt.Sprintf("Moved %d messages from %s to Gmail Trash. They remain recoverable for up to 30 days.", len(outcome.Moved), preview.Sender),
		Count:   len(outcome.Moved),
	})
}
