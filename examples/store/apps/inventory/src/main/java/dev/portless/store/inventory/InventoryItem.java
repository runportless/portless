package dev.portless.store.inventory;

public record InventoryItem(String sku, String name, int onHand, String warehouse) {
}
