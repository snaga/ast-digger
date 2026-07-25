package references;

public class CallerB {
    public static void useValue() {
        String message = "TargetMethod is not a reference here";
        Runnable ref = Target::TargetMethod;
        System.out.println(message);
        System.out.println(ref);
    }
}
