Feature: Notifications — a queued event bus

  Any component publishes; any component subscribes; the host wakes a
  subscriber when its lock is free, and queued events drain into one wake.

  Rule: Publishing queues and never blocks

    Scenario: Publishing returns to the publisher
      Given a subscriber of "job.completed" that never returns
      When another component publishes "job.completed"
      Then the publish returns without waiting for the subscriber

    Scenario: Per-subscriber order is publish order
      Given a subscriber of "job.tick"
      When three events are published to "job.tick"
      Then the subscriber receives them in publish order in one wake

  Rule: Wakes are host-driven and coalesce

    Scenario: Pending events cause one wake
      Given a subscriber of "job.completed" that is busy
      When two events are published to "job.completed"
      Then the subscriber is woken once after it returns
      And the wake carries both events

    Scenario: A wake never preempts a call in flight
      Given a subscriber that is mid-call
      When an event is published to its topic
      Then the wake waits until the call returns
      And the call is never interrupted

  Rule: The bus carries the layer's topics

    Scenario: Job and chat topics flow
      Given subscribers of every job topic and of "chat.message"
      When one event is published to each topic
      Then each subscriber receives its topic's event
