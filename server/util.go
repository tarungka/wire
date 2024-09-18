package server

import (
	"encoding/json"
	"net/http"
)

func createResponse(success bool, data interface{}, errorMsg string) ResponseModel {
	response := ResponseModel{
		Success: success,
		Data:    data,
		Error:   errorMsg,
	}
	return response
}

func SendResponse(w http.ResponseWriter, success bool, data interface{}, errorMsg string) {
	response := createResponse(success, data, errorMsg)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, `{"success":false,"error":"Internal Server Error"}`, http.StatusInternalServerError)
	}
}

func SendResponseWithHeader(w http.ResponseWriter, success bool, data interface{}, errorMsg string, statusCode int, payloadHeaders map[string]string) {
	response := createResponse(success, data, errorMsg)
	w.Header().Set("Content-Type", "application/json")

	// Set additional headers
	for key, value := range payloadHeaders {
		w.Header().Set(key, value)
	}

	// Set HTTP status code based on success
	if success {
		w.WriteHeader(http.StatusOK)
	} else {
		if statusCode != 0 {
			w.WriteHeader(statusCode)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, `{"success":false,"error":"Internal Server Error"}`, http.StatusInternalServerError)
	}
}
