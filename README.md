# conductor-service
**Conductor** is a consistent worker which consumes messages sent to a RabbitMQ channel in order to process MediaWiki jobs. This service was developed to replace the [Redis backed](https://github.com/Telepedia/mediawiki-services-jobrunner) job queue for Telepedia. 

Whilst the Redis backed queue is generally well behaved, it struggles when there are many different jobs and is often prone to errors and is sometimes slow. RabbitMQ provides a simple way to run jobs without the added complexity of running EventBus/EventGate and a Kafka cluster. The consumer (this service) is written in Go. It listens for jobs sent to any number of channels, and executes those jobs by calling out to a `.php` script running on Telepedia's internal cluster, MediaWiki takes over handling the job from there; once the job has been completed, a `200` code is returned back to Conductor, which acknowledges that the job has been completed and it is deleted from the RabbitMQ channel. 

### Configuration

### License
This project is licensed under the Apache License, Version 2.0. You may obtain a copy of the license at http://www.apache.org/licenses/LICENSE-2.0.

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.
