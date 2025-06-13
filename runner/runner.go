package runner

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/telepedia/conductor-service/utils"
)

type JobData struct {
	Type       string                 `json:"type"`
	Namespace  int                    `json:"namespace"`
	Title      string                 `json:"title"`
	Params     map[string]interface{} `json:"params"`
	RTimestamp int64                  `json:"rtimestamp"`
	UUID       string                 `json:"uuid"`
	SHA1       string                 `json:"sha1"`
	Timestamp  int64                  `json:"timestamp"`
	WikiID     string                 `json:"wikiId"`
}

func StartRunner() {
	utils.InitConfiguration()

	// Setup the RabbitMQ connection
	amqpURL := fmt.Sprintf("amqp://%s:%s@%s:%d/",
		utils.Config.RabbitMQ.User,
		utils.Config.RabbitMQ.Pass,
		utils.Config.RabbitMQ.Host,
		utils.Config.RabbitMQ.Port,
	)

	conn, err := amqp.Dial(amqpURL)
	utils.FailOnError(err, "Failed to connect to RabbitMQ")

	defer conn.Close()

	ch, err := conn.Channel()
	utils.FailOnError(err, "Failed to open a channel")
	defer ch.Close()

	// RabbitMQ does not support priorities easily, so we will hack it.
	// We have 4 queues:
	//  - 1 High Priority - these jobs must be processed as quickly as possible (CreateWiki jobs)
	//  - 2 Normal Priority - these jobs can be processed whenever a consumer is available (defaul for MediaWiki jobs)
	//  - 3 Low Priority  - these jobs are low priority, it does not matter when they are processed (we will potent. move this into normal)
	//  - 4 Ignored - These jobs are ignored and do not need to be processed. The consumer will ack them and remove them
	//  from the queue without processing them.
	highPriorityQueue := "mediawiki.jobs.high-priority"
	normalPriorityQueue := "mediawiki.jobs.normal-priority"
	lowPriorityQueue := "mediawiki.jobs.low-priority"
	ignoredQueue := "mediawiki.jobs.ignored"

	// Declare the topic exchange, this will handle routing job types to the correct queues
	exchangeName := utils.Config.RabbitMQ.ExchangeName
	if exchangeName == "" {
		// @TODO: pull this from config
		exchangeName = "mediawiki.jobs"
	}

	// Declare the queues
	_, err = ch.QueueDeclare(highPriorityQueue, true, false, false, false, nil)
	utils.FailOnError(err, "Failed to declare high priority queue")

	_, err = ch.QueueDeclare(normalPriorityQueue, true, false, false, false, nil)
	utils.FailOnError(err, "Failed to declare normal priority queue")

	_, err = ch.QueueDeclare(lowPriorityQueue, true, false, false, false, nil)
	utils.FailOnError(err, "Failed to declare low priority queue")

	_, err = ch.QueueDeclare(ignoredQueue, true, false, false, false, nil)
	utils.FailOnError(err, "Failed to declare ignored queue")

	// Bind high priority jobs first
	for _, routingKey := range utils.Config.Queues.HighPriority {
		err = ch.QueueBind(highPriorityQueue, routingKey, exchangeName, false, nil)
		utils.FailOnError(err, fmt.Sprintf("Failed to bind %s to high priority queue", routingKey))
	}

	// Bind low priority jobs
	for _, routingKey := range utils.Config.Queues.LowPriority {
		err = ch.QueueBind(lowPriorityQueue, routingKey, exchangeName, false, nil)
		utils.FailOnError(err, fmt.Sprintf("Failed to bind %s to low priority queue", routingKey))
	}

	// Bind ignored jobs
	for _, routingKey := range utils.Config.Queues.Ignored {
		err = ch.QueueBind(ignoredQueue, routingKey, exchangeName, false, nil)
		utils.FailOnError(err, fmt.Sprintf("Failed to bind %s to ignored queue", routingKey))
	}

	// Bind all jobs to normal priority queue as well
	// Again, RabbitMQ is a bit of an odd thing and we cannot say "send all jobs to this queue and not to this queue"
	// which is difficult to send jobs to the high priority queue and then fall back to the normal priority queue
	// for all other jobs, we MUST know what the job type is for this to work; as we cannot know what every single job
	// type is, we use mediawiki.job.* for the normal queue. We will segregate this later in the normal priority consumer
	// and ignore the job if it is not a normal priority job.
	// @TODO: potentially invesitgate this? if the job is picked up by the normal priority queue, could it already
	// have been processed before the high priority queue gets to it?
	err = ch.QueueBind(normalPriorityQueue, "mediawiki.job.*", exchangeName, false, nil)
	utils.FailOnError(err, "Failed to bind mediawiki.job.* to normal priority queue")

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Start consumers for each priority
	var wg sync.WaitGroup

	highWorkers := utils.Config.Workers.HighPriority
	if highWorkers == 0 {
		highWorkers = 3
	}
	for i := 0; i < highWorkers; i++ {
		wg.Add(1)
		go startConsumer(conn, highPriorityQueue, "high", i, &wg)
	}

	normalWorkers := utils.Config.Workers.NormalPriority
	if normalWorkers == 0 {
		normalWorkers = 2
	}
	for i := 0; i < normalWorkers; i++ {
		wg.Add(1)
		go startNormalConsumer(conn, normalPriorityQueue, "normal", i, &wg)
	}

	// We only need 1 worker for the low priority queue, these will be consumed when there is nothing else to do
	lowWorkers := utils.Config.Workers.LowPriority
	if lowWorkers == 0 {
		// @TODO: potentially pull this from config if we want more workers for the low priorty q?
		lowWorkers = 1
	}
	for i := 0; i < lowWorkers; i++ {
		wg.Add(1)
		go startConsumer(conn, lowPriorityQueue, "low", i, &wg)
	}

	// Start ignored queue consumer, again, we only need 1 worker for this
	// these jobs will not be processed, so it doesn't really matter
	// @TODO: as above
	wg.Add(1)
	go startIgnoredConsumer(conn, ignoredQueue, &wg)

	log.Println("All consumers started, waiting for messages...")

	// shutdown stuff
	<-sigs
	log.Println("Received shutdown signal, closing connections...")

	conn.Close()

	wg.Wait()
}

// This starts a consumer for the given queue and priority
func startConsumer(conn *amqp.Connection, queueName, priority string, workerID int, wg *sync.WaitGroup) {
	defer wg.Done()

	ch, err := conn.Channel()
	if err != nil {
		log.Printf("Failed to open channel for %s priority worker %d: %s", priority, workerID, err)
		return
	}
	defer ch.Close()

	// only fetch 1 message at a time
	err = ch.Qos(1, 0, false)
	if err != nil {
		log.Printf("Failed to set QoS for %s priority worker %d: %s", priority, workerID, err)
		return
	}

	msgs, err := ch.Consume(
		queueName,
		fmt.Sprintf("%s-%d", priority, workerID),
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("Failed to register consumer for %s priority worker %d: %s", priority, workerID, err)
		return
	}

	log.Printf("%s priority worker %d is ready", priority, workerID)

	for msg := range msgs {
		routingKey := msg.RoutingKey
		log.Printf("[%s-%d] Received message with routing key: %s", priority, workerID, routingKey)

		var jobData JobData
		if err := json.Unmarshal([]byte(msg.Body), &jobData); err != nil {
			log.Printf("[%s-%d] Error parsing job data: %v", priority, workerID, err)
			msg.Nack(false, true) // Nack and requeue if the job data is invalid
			continue
		}

		if handleJob(routingKey, jobData, priority, workerID) {
			msg.Ack(false)
		} else {
			// Nack and requeue if the job failed, this will
			// put the job back into the queue to be retried at a later time
			// this doesn't really have any impact on MediaWiki side of things since we won't
			// be tracking the number of jobs from MediaWiki - once a job is dispatched to the queue, MediaWiki
			// has no knowledge of it from that point (can probably fix that maybe?!)
			msg.Nack(false, true)
		}
	}

	log.Printf("%s priority worker %d shutting down", priority, workerID)
}

// Special consumer for normal priority that filters out jobs belonging to other queues
func startNormalConsumer(conn *amqp.Connection, queueName, priority string, workerID int, wg *sync.WaitGroup) {
	defer wg.Done()

	ch, err := conn.Channel()
	if err != nil {
		log.Printf("Failed to open channel for %s priority worker %d: %s", priority, workerID, err)
		return
	}
	defer ch.Close()

	err = ch.Qos(1, 0, false)
	if err != nil {
		log.Printf("Failed to set QoS for %s priority worker %d: %s", priority, workerID, err)
		return
	}

	msgs, err := ch.Consume(
		queueName,
		fmt.Sprintf("%s-%d", priority, workerID),
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("Failed to register consumer for %s priority worker %d: %s", priority, workerID, err)
		return
	}

	log.Printf("%s priority worker %d is ready", priority, workerID)

	for msg := range msgs {
		routingKey := msg.RoutingKey

		// hacky check: skip if this job belongs to a specific queue
		if utils.GetJobPriority(routingKey) != "normal" {
			log.Printf("[%s-%d] Skipping %s (belongs to a specific queue)", priority, workerID, routingKey)
			msg.Ack(false)
			continue
		}

		log.Printf("[%s-%d] Received message with routing key: %s", priority, workerID, routingKey)

		var jobData JobData
		if err := json.Unmarshal([]byte(msg.Body), &jobData); err != nil {
			log.Printf("[%s-%d] Error parsing job data: %v", priority, workerID, err)
			msg.Nack(false, true)
			continue
		}

		if handleJob(routingKey, jobData, priority, workerID) {
			msg.Ack(false)
		} else {
			msg.Nack(false, true)
		}
	}

	log.Printf("%s priority worker %d shutting down", priority, workerID)
}

func startIgnoredConsumer(conn *amqp.Connection, queueName string, wg *sync.WaitGroup) {
	defer wg.Done()

	ch, err := conn.Channel()
	if err != nil {
		log.Printf("Failed to open channel for ignored consumer: %s", err)
		return
	}
	defer ch.Close()

	msgs, err := ch.Consume(
		queueName,
		"ignored-0",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("Failed to register consumer for ignored queue: %s", err)
		return
	}

	log.Println("Ignored queue consumer is ready")

	for msg := range msgs {
		log.Printf("[ignored] Acknowledging and discarding message with routing key: %s", msg.RoutingKey)
		// This job is ignored, therefore just acknowledge it but do not process it
		// this removes it from the queue
		msg.Ack(false)
	}

	log.Println("Ignored queue consumer shutting down")
}

func handleJob(routingKey string, jobData JobData, priority string, workerID int) bool {
	log.Printf("[%s-%d] Processing job: type=%s, uuid=%s, wiki=%s",
		priority, workerID, jobData.Type, jobData.UUID, jobData.WikiID)

	// TODO: implement MediaWiki API call here
	// For now, just simulate work with different duration based on priority
	var sleepTime time.Duration
	switch priority {
	case "high":
		sleepTime = 1 * time.Second
	case "normal":
		sleepTime = 2 * time.Second
	case "low":
		sleepTime = 3 * time.Second
	}

	// Simulate work
	time.Sleep(sleepTime)

	log.Printf("[%s-%d] Finished job: type=%s, uuid=%s",
		priority, workerID, jobData.Type, jobData.UUID)

	return true
}
