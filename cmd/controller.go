package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/autoscaler"
	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/logger"
)

type ScaleRequest struct {
	TargetCapacity int            `json:"targetCapacity,omitempty"`
	Targets        map[string]int `json:"targets,omitempty"`
}

type ScaleResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

type StatusResponse struct {
	CurrentCapacity   int           `json:"currentCapacity"`
	Status            string        `json:"status"`
	TargetCapacity    int           `json:"targetCapacity"`
	ScalingInProgress bool          `json:"scalingInProgress"`
	LastActionTime    time.Time     `json:"lastActionTime"`
	InCooldownPeriod  bool          `json:"inCooldownPeriod"`
	CooldownRemaining time.Duration `json:"cooldownRemaining"`
	InstanceID        string        `json:"instanceId"`
	ResourceID        string        `json:"resourceId"`
	ResourceAlias     string        `json:"resourceAlias"`
}

var autoScaler *autoscaler.Autoscaler

func init() {
	// Initialize logger first
	logger.InitLogger()
}

func scaleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Parse request body
	var req ScaleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response := ScaleResponse{
			Success: false,
			Error:   fmt.Sprintf("Invalid JSON: %v", err),
		}
		w.WriteHeader(http.StatusBadRequest)
		err := json.NewEncoder(w).Encode(response)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to encode JSON response")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		return
	}

	// Validate target capacities
	if len(req.Targets) == 0 && req.TargetCapacity < 0 {
		response := ScaleResponse{
			Success: false,
			Error:   "Target capacity must be non-negative",
		}
		w.WriteHeader(http.StatusBadRequest)
		err := json.NewEncoder(w).Encode(response)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to encode JSON response")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		return
	}
	for resource, target := range req.Targets {
		if target < 0 {
			response := ScaleResponse{
				Success: false,
				Error:   fmt.Sprintf("Target capacity for resource %s must be non-negative", resource),
			}
			w.WriteHeader(http.StatusBadRequest)
			err := json.NewEncoder(w).Encode(response)
			if err != nil {
				logger.Warn().Err(err).Msg("Failed to encode JSON response")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			return
		}
	}

	// Perform scaling operation
	ctx := context.Background()
	var err error
	if len(req.Targets) > 0 {
		err = autoScaler.ScaleTargets(ctx, req.Targets)
	} else {
		err = autoScaler.ScaleToTarget(ctx, req.TargetCapacity)
	}
	if err != nil {
		logger.Warn().Err(err).Msg("Scaling failed")

		// Get current status to include in error response
		currentStatus, statusErr := statusForResponse(ctx)

		// Check if it's an "already in progress" error
		errMsg := err.Error()
		isInProgress := false
		if len(errMsg) >= 34 && errMsg[:34] == "scaling operation already in progress" {
			isInProgress = true
			errMsg = "A scaling operation is already in progress. Please wait for it to complete."
		} else {
			errMsg = fmt.Sprintf("Scaling failed: %v", err)
		}

		if statusErr == nil {
			// Include current status information in the error response
			response := map[string]interface{}{
				"success":       false,
				"error":         errMsg,
				"currentStatus": currentStatus,
			}
			// Use 409 Conflict for "already in progress" errors
			if isInProgress {
				w.WriteHeader(http.StatusConflict)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			err := json.NewEncoder(w).Encode(response)
			if err != nil {
				logger.Warn().Err(err).Msg("Failed to encode JSON response")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		} else {
			// If we can't get status, just return the error
			response := ScaleResponse{
				Success: false,
				Error:   errMsg,
			}
			if isInProgress {
				w.WriteHeader(http.StatusConflict)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			err := json.NewEncoder(w).Encode(response)
			if err != nil {
				logger.Warn().Err(err).Msg("Failed to encode JSON response")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		return
	}

	message := fmt.Sprintf("Successfully scaled to target capacity: %d", req.TargetCapacity)
	if len(req.Targets) > 0 {
		message = "Successfully requested grouped scaling targets"
	}
	response := ScaleResponse{
		Success: true,
		Message: message,
	}
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to encode JSON response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	ctx := r.Context()
	response, err := statusForResponse(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to get current capacity")
		response := ScaleResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to get current capacity: %v", err),
		}
		w.WriteHeader(http.StatusInternalServerError)
		err := json.NewEncoder(w).Encode(response)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to encode JSON response")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to encode JSON response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func statusForResponse(ctx context.Context) (interface{}, error) {
	config := autoScaler.GetConfig()
	if len(config.TargetResources) > 1 {
		return autoScaler.GetStatuses(ctx)
	}

	capacity, err := autoScaler.GetStatus(ctx)
	if err != nil {
		return nil, err
	}
	return StatusResponse{
		CurrentCapacity:   capacity.CurrentCapacity,
		Status:            string(capacity.Status),
		TargetCapacity:    capacity.TargetCapacity,
		ScalingInProgress: capacity.ScalingInProgress,
		LastActionTime:    capacity.LastActionTime,
		InCooldownPeriod:  capacity.InCooldownPeriod,
		CooldownRemaining: capacity.CooldownRemaining,
		InstanceID:        capacity.InstanceID,
		ResourceID:        capacity.ResourceID,
		ResourceAlias:     capacity.ResourceAlias,
	}, nil
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintf(w, `{"status": "healthy", "service": "autoscaler"}`)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to write health response")
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	config := autoScaler.GetConfig()
	_, err := fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
    <title>Omnistrate Autoscaler Control Panel</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap');
        
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        
        body {
            font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
            background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
            min-height: 100vh;
            display: flex;
            justify-content: center;
            align-items: center;
            padding: 20px;
        }
        
        .crt {
            position: relative;
            padding: 20px;
            max-width: 900px;
            width: 100%%;
        }
        
        .screen {
            background: #ffffff;
            border-radius: 16px;
            padding: 40px;
            box-shadow: 
                0 20px 60px rgba(0, 0, 0, 0.3),
                0 0 0 1px rgba(0, 0, 0, 0.05);
            position: relative;
        }
        
        .header {
            text-align: center;
            margin-bottom: 32px;
            position: relative;
        }
        
        h1 {
            font-size: 32px;
            font-weight: 700;
            letter-spacing: -0.5px;
            margin-bottom: 8px;
            color: #1a202c;
        }
        
        .subtitle {
            font-size: 14px;
            color: #718096;
            font-weight: 500;
            letter-spacing: 0.5px;
            text-transform: uppercase;
        }
        
        .config-box {
            background: linear-gradient(135deg, #f7fafc 0%%, #edf2f7 100%%);
            border: 1px solid #e2e8f0;
            border-radius: 12px;
            padding: 20px;
            margin: 20px 0;
            font-size: 14px;
        }
        
        .config-line {
            margin: 12px 0;
            display: flex;
            justify-content: space-between;
            line-height: 1.6;
        }
        
        .label { 
            color: #4a5568;
            font-weight: 500;
        }
        .value { 
            color: #2d3748;
            font-weight: 600;
            font-family: 'SF Mono', 'Monaco', 'Courier New', monospace;
        }
        
        .status-display {
            background: #f7fafc;
            border: 1px solid #e2e8f0;
            border-radius: 12px;
            padding: 24px;
            margin: 20px 0;
            min-height: 350px;
            font-size: 15px;
            color: #1a202c;
            font-weight: 500;
        }
        
        .status-line {
            margin: 10px 0;
            line-height: 1.8;
        }
        
        .status-line strong {
            color: #1a202c;
            font-weight: 700;
        }
        
        .controls {
            display: flex;
            flex-direction: column;
            gap: 12px;
            margin-top: 24px;
        }
        
        .control-group {
            display: flex;
            gap: 12px;
            align-items: stretch;
        }
        
        .control-group.scale-control {
            display: flex;
            gap: 12px;
        }
        
        .control-group.scale-control input {
            flex: 1;
            margin-bottom: 0;
        }
        
        .control-group.scale-control button {
            flex: 1;
            min-width: 200px;
        }
        
        .control-group.status-control {
            display: flex;
        }
        
        .control-group.status-control button {
            flex: 1;
        }
        
        button {
            font-family: 'Inter', sans-serif;
            font-size: 14px;
            font-weight: 600;
            padding: 14px 24px;
            background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
            color: #ffffff;
            border: none;
            border-radius: 8px;
            cursor: pointer;
            letter-spacing: 0.3px;
            transition: all 0.2s ease;
            box-shadow: 0 4px 12px rgba(102, 126, 234, 0.4);
            white-space: nowrap;
        }
        
        button:hover {
            transform: translateY(-2px);
            box-shadow: 0 6px 20px rgba(102, 126, 234, 0.5);
        }
        
        button:active {
            transform: translateY(0);
            box-shadow: 0 2px 8px rgba(102, 126, 234, 0.4);
        }
        
        input[type="number"] {
            font-family: 'Inter', sans-serif;
            font-size: 14px;
            font-weight: 500;
            padding: 14px 16px;
            background: #ffffff;
            color: #2d3748;
            border: 1px solid #cbd5e0;
            border-radius: 8px;
            margin-bottom: 10px;
            width: 100%%;
            transition: all 0.2s ease;
        }
        
        input[type="number"]:focus {
            outline: none;
            border-color: #667eea;
            box-shadow: 0 0 0 3px rgba(102, 126, 234, 0.1);
        }
        
        .loading {
            display: none;
            text-align: center;
            font-size: 14px;
            margin: 16px 0;
            color: #667eea;
            font-weight: 500;
            animation: pulse 1.5s ease-in-out infinite;
        }
        
        @keyframes pulse {
            0%%, 100%% { opacity: 1; }
            50%% { opacity: 0.5; }
        }
        
        .error { 
            color: #c53030;
            font-weight: 600;
        }
        .success { 
            color: #2f855a;
            font-weight: 600;
        }
        
        .timestamp {
            text-align: center;
            font-size: 12px;
            color: #718096;
            margin-top: 20px;
        }
    </style>
</head>
<body>
    <div class="crt">
        <div class="screen">
            <div class="header">
                <h1>Custom Autoscaler</h1>
                <div class="subtitle">Omnistrate Example</div>
            </div>
            
            <div class="config-box">
                <div class="config-line">
                    <span class="label">Target Resource</span>
                    <span class="value">%s</span>
                </div>
                <div class="config-line">
                    <span class="label">Cooldown Duration</span>
                    <span class="value">%v</span>
                </div>
            </div>
            
            <div class="status-display" id="statusDisplay">
                <div class="status-line">System ready</div>
                <div class="status-line">Waiting for command...</div>
            </div>
            
            <div class="loading" id="loading">Processing...</div>
            
            <div class="controls">
                <div class="control-group scale-control">
                    <input type="number" id="targetCapacity" placeholder="Enter target capacity" min="0" value="1">
                    <button onclick="scaleTarget()">Scale to Target</button>
                </div>
                <div class="control-group status-control">
                    <button onclick="getStatus()">Get Status</button>
                </div>
            </div>
            
            <div class="timestamp" id="timestamp"></div>
        </div>
    </div>
    
    <script>
        function updateTimestamp() {
            const now = new Date();
            document.getElementById('timestamp').textContent = 
                '◈ ' + now.toLocaleString('en-US', { 
                    year: 'numeric', month: '2-digit', day: '2-digit',
                    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false 
                }) + ' ◈';
        }
        
        setInterval(updateTimestamp, 1000);
        updateTimestamp();
        
        function showLoading(show) {
            document.getElementById('loading').style.display = show ? 'block' : 'none';
        }
        
        function displayStatus(content, isError = false) {
            const display = document.getElementById('statusDisplay');
            display.innerHTML = content;
            display.style.color = '#1a202c';
        }
        
        async function getStatus() {
            showLoading(true);
            try {
                const response = await fetch('/status');
                const data = await response.json();
                
                if (response.ok) {
                    const isFailed = data.status === 'FAILED';
                    const statusClass = isFailed ? 'error' : 'success';
                    
                    let statusDisplay = '<div class="status-line success"><strong>✓ Status Retrieved</strong></div>' +
                        '<div class="status-line" style="margin: 16px 0; border-top: 1px solid #e2e8f0;"></div>';
                    
                    // Resource Information
                    statusDisplay += '<div class="status-line ' + statusClass + '"><strong>Resource:</strong> ' + data.resourceAlias + '</div>';
                    statusDisplay += '<div class="status-line ' + statusClass + '"><strong>Status:</strong> ' + data.status + '</div>';
                    statusDisplay += '<div class="status-line ' + statusClass + '"><strong>Current Capacity:</strong> ' + data.currentCapacity + '</div>';
                    
                    // Only show target capacity if scaling is in progress
                    if (data.scalingInProgress) {
                        statusDisplay += '<div class="status-line ' + statusClass + '"><strong>Target Capacity:</strong> ' + data.targetCapacity + '</div>';
                        statusDisplay += '<div class="status-line" style="color: #667eea;">⚡ Scaling in progress...</div>';
                    }
                    
                    // Cooldown information
                    if (data.inCooldownPeriod) {
                        const cooldownSecs = Math.round(data.cooldownRemaining / 1000000000);
                        statusDisplay += '<div class="status-line" style="color: #ed8936;">⏱ Cooldown period: ' + cooldownSecs + 's remaining</div>';
                    }
                    
                    // Last action time if available
                    if (data.lastActionTime && data.lastActionTime !== '0001-01-01T00:00:00Z') {
                        const lastAction = new Date(data.lastActionTime);
                        const timeAgo = Math.round((new Date() - lastAction) / 1000);
                        let timeStr = timeAgo + 's ago';
                        if (timeAgo >= 60) {
                            timeStr = Math.round(timeAgo / 60) + 'm ago';
                        }
                        statusDisplay += '<div class="status-line"><strong>Last Action:</strong> ' + timeStr + '</div>';
                    }
                    
                    // Technical details
                    statusDisplay += '<div class="status-line" style="margin: 16px 0; border-top: 1px solid #e2e8f0;"></div>';
                    statusDisplay += '<div class="status-line" style="opacity: 0.6; font-size: 12px;"><strong>Instance ID:</strong> ' + data.instanceId + '</div>';
                    statusDisplay += '<div class="status-line" style="opacity: 0.6; font-size: 12px;"><strong>Resource ID:</strong> ' + data.resourceId + '</div>';
                    
                    displayStatus(statusDisplay);
                } else {
                    displayStatus(
                        '<div class="status-line error"><strong>✗ Error</strong></div>' +
                        '<div class="status-line error">' + (data.error || 'Unknown error') + '</div>',
                        true
                    );
                }
            } catch (error) {
                displayStatus(
                    '<div class="status-line error"><strong>✗ Connection Error</strong></div>' +
                    '<div class="status-line error">' + error.message + '</div>',
                    true
                );
            } finally {
                showLoading(false);
            }
        }
        
        async function scaleTarget() {
            const capacity = parseInt(document.getElementById('targetCapacity').value);
            
            if (isNaN(capacity) || capacity < 0) {
                displayStatus(
                    '<div class="status-line error">► INVALID INPUT</div>' +
                    '<div class="status-line error">Target capacity must be a non-negative number</div>',
                    true
                );
                return;
            }
            
            showLoading(true);
            try {
                const response = await fetch('/scale', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ targetCapacity: capacity })
                });
                
                const data = await response.json();
                
                if (response.ok && data.success) {
                    displayStatus(
                        '<div class="status-line success"><strong>✓ Scaling Successful</strong></div>' +
                        '<div class="status-line" style="margin: 16px 0; border-top: 1px solid #e2e8f0;"></div>' +
                        '<div class="status-line">' + data.message + '</div>' +
                        '<div class="status-line"><strong>Target Capacity:</strong> ' + capacity + '</div>'
                    );
                } else {
                    // If current status is included in the error response, display it first
                    let errorDisplay = '';
                    if (data.currentStatus) {
                        const isFailed = data.currentStatus.status === 'FAILED';
                        const statusClass = isFailed ? 'error' : 'success';
                        
                        errorDisplay = '<div class="status-line ' + statusClass + '"><strong>Current Status:</strong></div>' +
                            '<div class="status-line ' + statusClass + '"><strong>Resource:</strong> ' + data.currentStatus.resourceAlias + '</div>' +
                            '<div class="status-line ' + statusClass + '"><strong>Status:</strong> ' + data.currentStatus.status + '</div>' +
                            '<div class="status-line ' + statusClass + '"><strong>Capacity:</strong> ' + data.currentStatus.currentCapacity + '</div>' +
                            '<div class="status-line" style="margin: 16px 0; border-top: 1px solid #e2e8f0;"></div>';
                    }
                    
                    // Add error message below status
                    errorDisplay += '<div class="status-line error"><strong>✗ Scaling Failed</strong></div>' +
                        '<div class="status-line error">' + (data.error || 'Unknown error') + '</div>';
                    
                    displayStatus(errorDisplay, false);
                }
            } catch (error) {
                displayStatus(
                    '<div class="status-line error"><strong>✗ Connection Error</strong></div>' +
                    '<div class="status-line error">' + error.message + '</div>',
                    true
                );
            } finally {
                showLoading(false);
            }
        }
        
        // Allow Enter key to submit
        document.getElementById('targetCapacity').addEventListener('keypress', function(e) {
            if (e.key === 'Enter') {
                scaleTarget();
            }
        });
        
        // Automatically fetch status on page load
        document.addEventListener('DOMContentLoaded', function() {
            getStatus();
        });
    </script>
</body>
</html>
`, config.TargetResource, config.CooldownDuration)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to write HTML response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

/**
 * Autoscaler controller main function
 *
 * The controller reads configuration from environment variables:
 * - AUTOSCALER_COOLDOWN: Cooldown period in seconds (default: 300)
 * - AUTOSCALER_TARGET_RESOURCES: Comma-separated resource aliases to scale
 * - AUTOSCALER_TARGET_RESOURCE: Legacy single-resource alias fallback
 *
 * It exposes HTTP endpoints:
 * - POST /scale: Scale one resource or grouped resources to target capacity
 * - GET /status: Get current capacity and status
 * - GET /health: Health check
 *
 * The autoscaler will:
 * 1. Get current capacity using omnistrate_api
 * 2. Wait for instance to be ACTIVE if not already
 * 3. Respect cooldown period between scaling operations
 * 4. Add or remove capacity to match targets
 */
func main() {
	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initialize autoscaler
	var err error
	autoScaler, err = autoscaler.NewAutoscaler(ctx)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to initialize autoscaler")
	}
	logger.Info().Msg("Autoscaler initialized successfully")

	// Setup HTTP routes
	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/scale", scaleHandler)
	http.HandleFunc("/status", statusHandler)
	http.HandleFunc("/health", healthHandler)

	// Setup graceful shutdown
	chExit := make(chan os.Signal, 1)
	signal.Notify(chExit, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server in goroutine
	port := "3000"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	server := &http.Server{
		Addr:         ":" + port,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info().Str("port", port).Msg("Starting autoscaler controller")
		logger.Info().Msg("Environment variables required:")
		logger.Info().Msg("  - AUTOSCALER_TARGET_RESOURCES: Resource aliases to scale")
		logger.Info().Msg("  - AUTOSCALER_TARGET_RESOURCE: Legacy single-resource alias fallback")
		logger.Info().Msg("  - AUTOSCALER_COOLDOWN: Cooldown period in seconds (optional)")
		logger.Info().Msg("  - AUTOSCALER_STEPS: Number of steps for scaling (optional)")
		logger.Info().Msg("")
		logger.Info().Msg("Available endpoints:")
		logger.Info().Msg("  POST /scale - Scale to target capacity or grouped targets")
		logger.Info().Msg("  GET /status - Get current status")
		logger.Info().Msg("  GET /health - Health check")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("Server failed to start")
		}
	}()

	// Wait for shutdown signal
	<-chExit
	logger.Info().Msg("Shutting down gracefully...")
	cancel()

	// Shutdown server
	if err := server.Shutdown(ctx); err != nil {
		logger.Error().Err(err).Msg("Error during shutdown")
	}

	logger.Info().Msg("Autoscaler controller stopped")
}
