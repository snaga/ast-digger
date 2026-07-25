package references;

public class CallerA {
    public static void callSite() {
        Target.TargetMethod(); // actual reference on this line, this comment also mentions TargetMethod
    }
}
