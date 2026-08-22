package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/google/uuid"
)

func authenticatedRecoveryPlanningActor(
	r *http.Request,
) (service.RecoveryPlanningActor, error) {
	claims, _ :=
		r.Context().Value(ctxKeyClaims).(*service.Claims)

	if claims == nil {
		return service.RecoveryPlanningActor{},
			domain.NewUnauthorized(
				"not authenticated",
			)
	}

	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.RecoveryPlanningActor{},
			domain.NewUnauthorized(
				"invalid authenticated user id",
			)
	}

	return service.RecoveryPlanningActor{
		UserID:   id,
		Username: claims.Username,
		Role:     string(claims.Role),
	}, nil
}

func (h *server) listRecoveryScenarios(
	w http.ResponseWriter,
	r *http.Request,
) {
	rows, err :=
		h.s.RecoveryPlanning.ListScenarios(
			r.Context(),
			strings.TrimSpace(
				r.URL.Query().Get("status"),
			),
		)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	writeJSON(
		w,
		http.StatusOK,
		rows,
	)
}

func (h *server) createRecoveryScenario(
	w http.ResponseWriter,
	r *http.Request,
) {
	var body service.RecoveryScenarioCreateInput

	if err :=
		json.NewDecoder(r.Body).Decode(
			&body,
		); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	actor, err :=
		authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(
			w,
			http.StatusUnauthorized,
			err,
		)
		return
	}

	row, err :=
		h.s.RecoveryPlanning.CreateScenario(
			r.Context(),
			body,
			actor,
		)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	writeJSON(
		w,
		http.StatusCreated,
		row,
	)
}

func (h *server) getRecoveryScenario(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	row, err :=
		h.s.RecoveryPlanning.GetScenario(
			r.Context(),
			id,
		)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	writeJSON(
		w,
		http.StatusOK,
		row,
	)
}

func (h *server) updateRecoveryScenario(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	var body service.RecoveryScenarioUpdateInput

	if err :=
		json.NewDecoder(r.Body).Decode(
			&body,
		); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	actor, err :=
		authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(
			w,
			http.StatusUnauthorized,
			err,
		)
		return
	}

	row, err :=
		h.s.RecoveryPlanning.UpdateScenario(
			r.Context(),
			id,
			body,
			actor,
		)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	writeJSON(
		w,
		http.StatusOK,
		row,
	)
}

func (h *server) archiveRecoveryScenario(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	actor, err :=
		authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(
			w,
			http.StatusUnauthorized,
			err,
		)
		return
	}

	row, err :=
		h.s.RecoveryPlanning.ArchiveScenario(
			r.Context(),
			id,
			actor,
		)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	writeJSON(
		w,
		http.StatusOK,
		row,
	)
}

func (h *server) listRecoveryScenarioActions(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	rows, err :=
		h.s.RecoveryPlanning.ListScenarioActions(
			r.Context(),
			id,
		)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	writeJSON(
		w,
		http.StatusOK,
		rows,
	)
}

func (h *server) addRecoveryScenarioAction(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	var body service.RecoveryScenarioActionInput

	if err :=
		json.NewDecoder(r.Body).Decode(
			&body,
		); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	actor, err :=
		authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(
			w,
			http.StatusUnauthorized,
			err,
		)
		return
	}

	row, err :=
		h.s.RecoveryPlanning.AddScenarioAction(
			r.Context(),
			id,
			body,
			actor,
		)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	writeJSON(
		w,
		http.StatusCreated,
		row,
	)
}

func (h *server) updateRecoveryScenarioAction(
	w http.ResponseWriter,
	r *http.Request,
) {
	scenarioID, err := parseUUID(r, "id")
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	actionID, err :=
		parseUUID(r, "actionId")
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	var body service.RecoveryScenarioActionInput

	if err :=
		json.NewDecoder(r.Body).Decode(
			&body,
		); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	actor, err :=
		authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(
			w,
			http.StatusUnauthorized,
			err,
		)
		return
	}

	row, err :=
		h.s.RecoveryPlanning.UpdateScenarioAction(
			r.Context(),
			scenarioID,
			actionID,
			body,
			actor,
		)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	writeJSON(
		w,
		http.StatusOK,
		row,
	)
}

func (h *server) deleteRecoveryScenarioAction(
	w http.ResponseWriter,
	r *http.Request,
) {
	scenarioID, err := parseUUID(r, "id")
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	actionID, err :=
		parseUUID(r, "actionId")
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	actor, err :=
		authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(
			w,
			http.StatusUnauthorized,
			err,
		)
		return
	}

	if err :=
		h.s.RecoveryPlanning.DeleteScenarioAction(
			r.Context(),
			scenarioID,
			actionID,
			actor,
		); err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *server) simulateRecoveryScenario(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	body := service.RecoverySimulationRequest{
		ScenarioID: id,
	}

	if r.ContentLength != 0 {
		if err :=
			json.NewDecoder(r.Body).Decode(
				&body,
			); err != nil {
			writeError(
				w,
				http.StatusBadRequest,
				err,
			)
			return
		}

		body.ScenarioID = id
	}

	actor, err :=
		authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(
			w,
			http.StatusUnauthorized,
			err,
		)
		return
	}

	row, err :=
		h.s.RecoveryPlanning.Simulate(
			r.Context(),
			body,
			actor,
		)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	writeJSON(
		w,
		http.StatusCreated,
		row,
	)
}

func (h *server) compareRecoveryScenarios(
	w http.ResponseWriter,
	r *http.Request,
) {
	rows, err :=
		h.s.RecoveryPlanning.CompareScenarios(
			r.Context(),
			strings.TrimSpace(
				r.URL.Query().Get(
					"baselineHash",
				),
			),
		)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	writeJSON(
		w,
		http.StatusOK,
		rows,
	)
}

func (h *server) publishRecoveryScenario(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	var body service.RecoveryScenarioPublishInput

	if err :=
		json.NewDecoder(r.Body).Decode(
			&body,
		); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			err,
		)
		return
	}

	actor, err :=
		authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(
			w,
			http.StatusUnauthorized,
			err,
		)
		return
	}

	row, err :=
		h.s.RecoveryPlanning.PublishScenario(
			r.Context(),
			id,
			body,
			actor,
		)

	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			err,
		)
		return
	}

	writeJSON(
		w,
		http.StatusCreated,
		row,
	)
}
