A CI task issue has the title `Fix the failed workflow "<workflow>" on <branch>`. Its body starts with `The workflow "<workflow>" failed on <branch>.` If the cause is outside the code, for example an outage of a third-party service or a GitHub runner failure, do these steps. The same rule applies when the Implementer of the CI task calls `cannot_do` for such a cause.

1. Call `close_task` with the cause and the proof as the reason. The proof is the log line or the status page. The close ends the stop of the dispatch.
2. Write the cause in your memory, with the date and the link to the run.
