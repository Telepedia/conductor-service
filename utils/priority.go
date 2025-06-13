package utils

func GetJobPriority(routingKey string) string {
	// Check if job is in high priority list
	for _, queue := range Config.Queues.HighPriority {
		if queue == routingKey {
			return "high"
		}
	}

	// Check if job is in low priority list
	for _, queue := range Config.Queues.LowPriority {
		if queue == routingKey {
			return "low"
		}
	}

	// Check if job is in ignored list
	for _, queue := range Config.Queues.Ignored {
		if queue == routingKey {
			return "ignored"
		}
	}

	// Default to normal priority
	return "normal"
}

// Utility function to check if a job is in the ignored list
// if it is, it will be acknowledged, and removed from the queue
// without being processed - useful for jobs that are not needed
// but that MediaWiki won't stop sending
func IsIgnoredJob(routingKey string) bool {
	return GetJobPriority(routingKey) == "ignored"
}
