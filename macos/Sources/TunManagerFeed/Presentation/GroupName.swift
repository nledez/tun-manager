/// The groups tun-manager gives meaning to.
///
/// The wire carries whatever the configuration says, so a group is just a
/// string — but these two decide what is started by default and what is merely
/// offered, and the menu orders by that rather than by the alphabet.
public enum GroupName {
    public static let needed = "needed"
    public static let extra = "extra"
}
