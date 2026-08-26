package dev.portless.store.inventory;

import java.sql.ResultSet;
import java.sql.SQLException;
import java.time.Instant;
import java.util.List;
import java.util.Locale;
import java.util.Optional;

import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;
import org.springframework.transaction.support.TransactionTemplate;

import jakarta.annotation.PostConstruct;

@Service
public class InventoryCatalog {
    private static final List<InventoryItem> DEFAULT_ITEMS = List.of(
            new InventoryItem("coffee-mug", "Ceramic Coffee Mug", 24, "ord-01"),
            new InventoryItem("mechanical-keyboard", "Mechanical Keyboard", 8, "ord-01"),
            new InventoryItem("usb-c-cable", "USB-C Cable", 0, "dfw-02"));

    static final String CREATE_INVENTORY_SQL = """
            CREATE TABLE IF NOT EXISTS store_inventory (
              sku TEXT PRIMARY KEY,
              name TEXT NOT NULL,
              on_hand INTEGER NOT NULL CHECK (on_hand >= 0),
              warehouse TEXT NOT NULL
            )
            """;
    static final String CREATE_RESERVATIONS_SQL = """
            CREATE TABLE IF NOT EXISTS store_inventory_reservations (
              id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
              sku TEXT NOT NULL REFERENCES store_inventory(sku),
              quantity INTEGER NOT NULL CHECK (quantity > 0),
              state TEXT NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved', 'released')),
              created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
              released_at TIMESTAMPTZ
            )
            """;
    static final String RESERVE_STOCK_SQL = """
            UPDATE store_inventory
            SET on_hand = on_hand - ?
            WHERE sku = ? AND on_hand >= ?
            RETURNING sku, name, on_hand, warehouse
            """;
    static final String INSERT_RESERVATION_SQL = """
            INSERT INTO store_inventory_reservations (sku, quantity)
            VALUES (?, ?)
            RETURNING id, sku, quantity, state, created_at
            """;
    static final String RELEASE_RESERVATION_SQL = """
            UPDATE store_inventory_reservations
            SET state = 'released', released_at = now()
            WHERE id = ? AND state = 'reserved'
            RETURNING sku, quantity
            """;

    private static final String SELECT_ITEMS_SQL = """
            SELECT sku, name, on_hand, warehouse
            FROM store_inventory
            ORDER BY sku
            """;
    private static final String SELECT_ITEM_SQL = """
            SELECT sku, name, on_hand, warehouse
            FROM store_inventory
            WHERE sku = ?
            """;
    private static final String SEED_ITEM_SQL = """
            INSERT INTO store_inventory (sku, name, on_hand, warehouse)
            VALUES (?, ?, ?, ?)
            ON CONFLICT (sku) DO NOTHING
            """;
    private static final String RESET_ITEM_SQL = """
            INSERT INTO store_inventory (sku, name, on_hand, warehouse)
            VALUES (?, ?, ?, ?)
            ON CONFLICT (sku) DO UPDATE
            SET name = EXCLUDED.name,
                on_hand = EXCLUDED.on_hand,
                warehouse = EXCLUDED.warehouse
            """;

    private final JdbcTemplate jdbc;
    private final TransactionTemplate transactions;

    public InventoryCatalog(JdbcTemplate jdbc, TransactionTemplate transactions) {
        this.jdbc = jdbc;
        this.transactions = transactions;
    }

    @PostConstruct
    void migrate() {
        jdbc.execute(CREATE_INVENTORY_SQL);
        jdbc.execute(CREATE_RESERVATIONS_SQL);
        DEFAULT_ITEMS.forEach(this::seed);
    }

    public List<InventoryItem> items() {
        return jdbc.query(SELECT_ITEMS_SQL, InventoryCatalog::inventoryItem);
    }

    public Optional<InventoryAvailability> availability(String sku, int requested) {
        List<InventoryItem> matches = jdbc.query(
                SELECT_ITEM_SQL,
                InventoryCatalog::inventoryItem,
                sku.toLowerCase(Locale.ROOT));
        if (matches.isEmpty()) {
            return Optional.empty();
        }
        InventoryItem item = matches.get(0);
        return Optional.of(new InventoryAvailability(
                item.sku(), item.name(), requested, item.onHand(),
                item.onHand() >= requested, item.warehouse()));
    }

    public InventoryReservationOutcome reserve(String sku, int quantity) {
        String normalizedSKU = sku.toLowerCase(Locale.ROOT);
        return transactions.execute(status -> {
            List<InventoryItem> updated = jdbc.query(
                    RESERVE_STOCK_SQL,
                    InventoryCatalog::inventoryItem,
                    quantity, normalizedSKU, quantity);
            if (updated.isEmpty()) {
                return availability(normalizedSKU, quantity)
                        .map(InventoryReservationOutcome::insufficient)
                        .orElseGet(InventoryReservationOutcome::notFound);
            }
            InventoryReservation reservation = jdbc.query(
                    INSERT_RESERVATION_SQL,
                    InventoryCatalog::reservation,
                    normalizedSKU, quantity).get(0);
            return InventoryReservationOutcome.reserved(
                    new InventoryReservationResult(reservation, updated.get(0)));
        });
    }

    public boolean release(long reservationID) {
        Boolean released = transactions.execute(status -> {
            List<ReleasedStock> rows = jdbc.query(
                    RELEASE_RESERVATION_SQL,
                    (result, row) -> new ReleasedStock(result.getString("sku"), result.getInt("quantity")),
                    reservationID);
            if (!rows.isEmpty()) {
                ReleasedStock stock = rows.get(0);
                jdbc.update("UPDATE store_inventory SET on_hand = on_hand + ? WHERE sku = ?", stock.quantity(), stock.sku());
                return true;
            }
            Integer count = jdbc.queryForObject(
                    "SELECT count(*) FROM store_inventory_reservations WHERE id = ?",
                    Integer.class,
                    reservationID);
            return count != null && count > 0;
        });
        return Boolean.TRUE.equals(released);
    }

    public List<InventoryItem> reset() {
        return transactions.execute(status -> {
            jdbc.execute("LOCK TABLE store_inventory, store_inventory_reservations IN SHARE ROW EXCLUSIVE MODE");
            for (InventoryItem item : DEFAULT_ITEMS) {
                jdbc.update(RESET_ITEM_SQL, item.sku(), item.name(), item.onHand(), item.warehouse());
            }
            jdbc.update("DELETE FROM store_inventory_reservations");
            return items();
        });
    }

    private void seed(InventoryItem item) {
        jdbc.update(SEED_ITEM_SQL, item.sku(), item.name(), item.onHand(), item.warehouse());
    }

    private static InventoryItem inventoryItem(ResultSet result, int row) throws SQLException {
        return new InventoryItem(
                result.getString("sku"),
                result.getString("name"),
                result.getInt("on_hand"),
                result.getString("warehouse"));
    }

    private static InventoryReservation reservation(ResultSet result, int row) throws SQLException {
        return new InventoryReservation(
                result.getLong("id"),
                result.getString("sku"),
                result.getInt("quantity"),
                result.getString("state"),
                result.getTimestamp("created_at").toInstant());
    }

    private record ReleasedStock(String sku, int quantity) {
    }
}
