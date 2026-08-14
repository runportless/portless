package dev.portless.store.inventory;

public record InventoryAvailability(
        String sku,
        String name,
        int requested,
        int onHand,
        boolean available,
        String warehouse) {
}
