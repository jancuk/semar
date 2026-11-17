Feature: SHOP-1

  @id:SHOP-1-02 @ticket:SHOP-1 @surface:api @auth:support
  Scenario: A refund above the total is refused
    Before, any refund was accepted.
    Now a refund above the total gives 422.

    Given the fixture order.paid as order with an exclusive lease
      | amount   | 1000 |
      | customer | c-1  |
    And the fixture customer.verified as customer
    And Create a paid order
    # step: create; POST /v1/orders
    And as bob, Bob asks for a quote
    # step: ask; GET /v1/quotes
    When as bob, Ask for a refund of 150000
    # step: refund; POST /v1/orders/{id}/refunds
    Then the status is 422
    # ticket docs/SHOP-1.md:12 a refund above the total is refused, by model
    And $.error.code equals "REFUND_EXCEEDS_TOTAL"
    # diff
    And the header Content-Type contains "json"
    # person
    Given Open the order page
    # step: open
    When Read the order again
    # step: look; GET /v1/orders/{id}
    Then $.data.refunded_total equals 0
    # ticket SHOP-1: the order stays unchanged
    And the element total equals "{{order.total}}"
    # baseline
