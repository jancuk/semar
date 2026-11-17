Feature: SHOP-482

  @id:SHOP-482-05 @ticket:SHOP-482 @surface:api @auth:support-agent @intent-newline
  Scenario: A paid order is read back as paid
    \Given a paid order, its owner reads it and sees the status paid and the amount that was paid.

    Given the fixture order.paid as order
    When Read the paid order
    # step: read-paid-order; GET /v1/orders/{id}
    Then the status is 200
    # ticket SHOP-482: a paid order can be read by its owner
    And $.data.status equals "paid"
    # ticket SHOP-482: the order stays paid
    And $.data.amount equals 1000
    # ticket SHOP-482: the paid amount is the order amount
