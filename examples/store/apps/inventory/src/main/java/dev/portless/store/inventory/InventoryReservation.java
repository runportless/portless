package dev.portless.store.inventory;

import java.time.Instant;

public record InventoryReservation(long id, String sku, int quantity, String state, Instant createdAt) {
}
