Feature: Jobs overlay

  As the writer
  I want the launch's jobs in an overlay I can summon
  so that I can see what is running and peek at a job's output without
  leaving the chat

  Background:
    Given nvchat is running

  Rule: The overlay is read-only

    Scenario: Nothing in the overlay controls a job
      Given the launch has a running job "job-1"
      When the writer opens the jobs overlay
      Then the overlay lists "job-1"
      And no key in the overlay starts, kills, or otherwise controls a job

    Scenario: Ticks never join the transcript
      Given a job "job-1" ticks
      Then the message list gains no entry
      And the jobs overlay may show "job-1"'s last tick in its peek

  Rule: The overlay shows the tree and the selected job's peek

    Scenario: Listing the launch's jobs as a tree
      Given the launch has the jobs "job-1" and its child "job-2"
      When the writer opens the jobs overlay
      Then the overlay nests "job-2" under "job-1"
      And each row shows id, tool, state, and age

    Scenario: Peeking the selected job
      Given the launch has a running job "job-1" with output so far
      When the writer selects "job-1"
      Then the overlay shows its state, last tick, and output so far

  Rule: The overlay toggles without losing its place

    Scenario: Closing the overlay
      Given the jobs overlay is open
      When the writer closes the jobs overlay
      Then the jobs overlay is hidden
      And the workspace is unchanged

    Scenario: Reopening the overlay keeps its place
      Given the jobs overlay is open
      And the jobs cursor is on "job-1"
      When the writer closes the jobs overlay
      And the writer opens the jobs overlay
      Then the jobs cursor is on "job-1"

  Rule: The overlay refreshes as transitions arrive

    Scenario: A job changes state while the overlay is open
      Given the jobs overlay is open
      When the job "job-1" completes
      Then the overlay refreshes "job-1"'s row
