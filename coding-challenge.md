Site Intelligence Backend Engineer – Code Challenge
Context & Goal
Your team processes access and bot logs from customer websites. Each customer installs an NPM package that forwards logs to your backend through a simple dump API.
The backend stores logs into storage buckets and a data pipeline processes these files and loads them into an analytics database for analysis.
Your task is to simulate a simplified version of this ingestion and aggregation pipeline.
We want to see how you structure your code, handle data, and, most importantly,  how you think about scalability and reliability.

Task Overview
Implement a small log ingestion and aggregation service in Node.js or Go (your choice), that should:
Expose an HTTP API that receives batches of logs.
Store them locally (as files or in memory, simulating cloud storage).
Periodically process the stored data to compute simple aggregations.
Output the results into an “analytics layer” (for example, another JSON file).
Optionally, you can extend the design to demonstrate how it could scale under production load (f.e. queue-based ingestion, sharding, etc.).

Functional Requirements 

1. API Endpoint: Implement an endpoint: POST /logs that should:
accept a JSON or CSV array of log entries
Validate the payload (missing fields, invalid timestamps, etc.).
Simulate writing to a bucket (e.g., a new .json file per batch or a folder-based structure).
Return a 202 Accepted status when the batch is stored successfully.
2. Aggregation Task: Implement a background or triggerable task that:
Reads stored batches (simulating your raw bucket data).
Aggregates logs, for example:
Count requests per path
Count requests per userAgent
Outputs the aggregated result into a separate file
3. Observability: Implement basic metrics to expose internal system information compatible with Prometheus, such as:
Total number of received log batches
Number of invalid requests
Number of processed log files or aggregation runs
(you can expose these metrics at an endpoint GET /metrics)
4. Error Handling: show robust error handling:
Invalid payloads should return an informative error message.
Corrupted files or I/O errors should be logged, not crash the service.
Partial failures should be tolerated gracefully
5. (Optional Bonus) Scalability Simulation: simulate how this design could scale:
Replace local storage with a message queue or buffer (Pub/Sub, Kafka, Redis, etc.).
Parallelize the aggregation process.
Or provide a diagram + explanation in your README describing the scalable version

Technical Requirements
Language: Node.js or Go 
Use any standard frameworks 
Add unit tests for your core logic (validation, aggregation, etc.)
Use Docker optionally (bonus)
Code must be readable, modular, and production-minded

Submission
Please provide your solution as a GitHub repository or ZIP file, including:
Source code
Tests
README.md with:
How to run the project
How the code is structured
What scalability or reliability improvements you’d suggest for a production version
(Optional) a small architecture diagram
Note on any AI tools used

Evaluation Criteria & Final Thoughts
Evaluation Criteria:
Correctness: Implements required functionality
Code Quality: Clean, maintainable, modular
Resilience: Handles errors and edge cases
Scalability Thinking: Identifies weak points and proposes realistic improvements
Clarity: README, documentation, and design reasoning

Estimated Effort
Approx. 2–3 hours.
Focus on implementing the core ingestion, aggregation, and observability logic.
You don’t need to build a production-grade infrastructure, but you should demonstrate awareness of scalability, observability, and resilience in your approach.
If you have additional ideas for how to make the system more scalable or robust, describe them briefly in the README, these topics will be discussed further during the technical interview.